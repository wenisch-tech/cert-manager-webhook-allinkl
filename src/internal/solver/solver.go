package solver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	whapi "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/kas"
)

// apiTimeout bounds a single Present/CleanUp. It is generous because the KAS
// client may legitimately sit out one or more flood-protection delays before
// its call goes through.
const apiTimeout = 3 * time.Minute

// kasConfig is the per-issuer `config` block from the ClusterIssuer's
// dns01.webhook stanza.
type kasConfig struct {
	// ZoneName overrides the zone cert-manager derives from an SOA lookup.
	// Normally unnecessary: for lan.example.com cert-manager already resolves
	// the zone to example.com, which is what KAS expects. Set it when the
	// derived zone and the KAS zone genuinely differ.
	ZoneName string `json:"zoneName,omitempty"`

	// Credentials for the KAS account. Both Secrets are read from the
	// namespace cert-manager runs the challenge in.
	UserSecretRef     cmmeta.SecretKeySelector `json:"userSecretRef"`
	PasswordSecretRef cmmeta.SecretKeySelector `json:"passwordSecretRef"`

	// MaxRetries bounds flood-protection retries per KAS call. Zero uses the
	// client default of 5.
	MaxRetries int `json:"maxRetries,omitempty"`
}

type Solver struct {
	kube kubernetes.Interface
}

// New returns the solver cert-manager will call.
func New() *Solver { return &Solver{} }

// Name is the solverName referenced from the issuer's webhook config.
func (s *Solver) Name() string { return "allinkl" }

// Initialize is called once by the webhook framework at startup.
func (s *Solver) Initialize(cfg *rest.Config, _ <-chan struct{}) error {
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}
	s.kube = kube
	return nil
}

// Present creates the challenge TXT record.
func (s *Solver) Present(ch *whapi.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	client, zone, name, err := s.setup(ctx, ch)
	if err != nil {
		return err
	}

	// cert-manager can call Present more than once for the same challenge
	// (retries, controller restarts, a re-queued Order). KAS appends
	// unconditionally rather than upserting, so without this check duplicate
	// records pile up -- and since CleanUp removes what it finds, a stale
	// duplicate left behind would keep answering after the challenge is over.
	records, err := client.GetRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("listing records in %s: %w", zone, err)
	}
	for _, r := range records {
		if matches(r, name, zone, ch.Key) {
			return nil
		}
	}

	if _, err := client.AddTXTRecord(ctx, zone, name, ch.Key); err != nil {
		return fmt.Errorf("creating TXT %s in %s: %w", name, zone, err)
	}
	return nil
}

// CleanUp removes the challenge TXT record. It must tolerate the record
// already being gone: cert-manager calls CleanUp on paths where Present never
// completed.
func (s *Solver) CleanUp(ch *whapi.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	client, zone, name, err := s.setup(ctx, ch)
	if err != nil {
		return err
	}

	records, err := client.GetRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("listing records in %s: %w", zone, err)
	}

	for _, r := range records {
		if !matches(r, name, zone, ch.Key) {
			continue
		}
		if err := client.DeleteRecord(ctx, r.ID); err != nil {
			return fmt.Errorf("deleting record %s: %w", r.ID, err)
		}
	}
	return nil
}

// setup resolves everything a single challenge operation needs: an
// authenticated KAS client, the KAS zone host, and the record name relative
// to that zone.
func (s *Solver) setup(ctx context.Context, ch *whapi.ChallengeRequest) (*kas.Client, string, string, error) {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return nil, "", "", err
	}

	user, err := s.secretValue(ctx, ch.ResourceNamespace, cfg.UserSecretRef)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading KAS user: %w", err)
	}
	password, err := s.secretValue(ctx, ch.ResourceNamespace, cfg.PasswordSecretRef)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading KAS password: %w", err)
	}

	zone := cfg.ZoneName
	if zone == "" {
		zone = ch.ResolvedZone
	}
	if zone == "" {
		return nil, "", "", fmt.Errorf("could not determine the KAS zone; set zoneName in the webhook config")
	}
	zone = ensureTrailingDot(zone)

	name, err := relativeName(ch.ResolvedFQDN, zone)
	if err != nil {
		return nil, "", "", err
	}

	client := &kas.Client{User: user, Password: password, MaxRetries: cfg.MaxRetries}
	return client, zone, name, nil
}

func loadConfig(raw *extapi.JSON) (kasConfig, error) {
	cfg := kasConfig{}
	if raw == nil {
		return cfg, fmt.Errorf("no webhook config provided; userSecretRef and passwordSecretRef are required")
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decoding webhook config: %w", err)
	}
	if cfg.UserSecretRef.Name == "" || cfg.PasswordSecretRef.Name == "" {
		return cfg, fmt.Errorf("both userSecretRef and passwordSecretRef must be set")
	}
	return cfg, nil
}

func (s *Solver) secretValue(ctx context.Context, namespace string, ref cmmeta.SecretKeySelector) (string, error) {
	if s.kube == nil {
		return "", fmt.Errorf("solver not initialized")
	}
	secret, err := s.kube.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return strings.TrimSpace(string(value)), nil
}

// relativeName converts the fully-qualified challenge name into the name KAS
// wants, which is relative to the zone: _acme-challenge.lan.example.com. in
// zone example.com. becomes _acme-challenge.lan.
func relativeName(fqdn, zone string) (string, error) {
	f := strings.TrimSuffix(strings.TrimSpace(fqdn), ".")
	z := strings.TrimSuffix(strings.TrimSpace(zone), ".")
	if f == "" {
		return "", fmt.Errorf("empty challenge FQDN")
	}
	if !strings.HasSuffix(strings.ToLower(f), "."+strings.ToLower(z)) {
		return "", fmt.Errorf("challenge name %q is not inside zone %q", fqdn, zone)
	}
	return f[:len(f)-len(z)-1], nil
}

func ensureTrailingDot(zone string) string {
	zone = strings.TrimSpace(zone)
	if strings.HasSuffix(zone, ".") {
		return zone
	}
	return zone + "."
}

// matches reports whether a KAS record is the challenge record we manage.
// KAS is inconsistent about whether record_name comes back relative or fully
// qualified, and about quoting TXT payloads, so both are normalised here
// rather than trusted.
func matches(r kas.Record, name, zone, key string) bool {
	if !strings.EqualFold(r.Type, "TXT") {
		return false
	}
	if unquote(r.Data) != key {
		return false
	}
	return strings.EqualFold(normalizeName(r.Name, zone), name)
}

func normalizeName(recordName, zone string) string {
	n := strings.TrimSuffix(strings.TrimSpace(recordName), ".")
	z := strings.TrimSuffix(strings.TrimSpace(zone), ".")
	if strings.EqualFold(n, z) {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(n), "."+strings.ToLower(z)) {
		return n[:len(n)-len(z)-1]
	}
	return n
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}
