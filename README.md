# cert-manager-webhook-allinkl

A [cert-manager](https://cert-manager.io) ACME **DNS-01 solver webhook** for
domains hosted at **All-Inkl** ([kasserver.com](https://kasserver.com)), driving
their KAS API.

cert-manager ships no in-tree provider for All-Inkl, and no community webhook
existed. This fills that gap, which unlocks two things HTTP-01 cannot do:

- **Wildcard certificates** (`*.example.com`) — only ever issuable over DNS-01.
- **Certificates for names that never resolve publicly**, such as LAN-only
  hostnames. DNS-01 validates a `_acme-challenge` TXT record; it never looks at
  the A record of the name being certified, so internal addresses stay out of
  public DNS.

## Install

```bash
helm repo add jfwenisch https://jfwenisch.github.io/charts
helm install cert-manager-webhook-allinkl jfwenisch/cert-manager-webhook-allinkl \
  --namespace cert-manager \
  --set groupName=acme.example.com
```

`groupName` must be a domain **you** control. It becomes the aggregated API
group this webhook serves, and it must match `groupName` in your issuer. The
chart refuses to render without it.

### Credentials

The chart deliberately does not manage the Secret — these credentials control
every DNS record in the All-Inkl account. Create a dedicated KAS API user in
the All-Inkl panel rather than reusing your main login, so it can be revoked
independently:

```bash
kubectl create secret generic allinkl-api-credentials \
  --namespace cert-manager \
  --from-literal=user='<KAS_USER>' \
  --from-literal=password='<KAS_PASSWORD>'
```

For a `ClusterIssuer`, the Secret must live in cert-manager's
`--cluster-resource-namespace` (`cert-manager` by default).

### Issuer

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-allinkl
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: letsencrypt-allinkl-account-key
    solvers:
      - dns01:
          webhook:
            groupName: acme.example.com
            solverName: allinkl
            config:
              # The KAS zone, which is the registrable domain -- a name under
              # lan.example.com still lives in the example.com zone.
              zoneName: example.com.
              userSecretRef:
                name: allinkl-api-credentials
                key: user
              passwordSecretRef:
                name: allinkl-api-credentials
                key: password
        selector:
          dnsZones:
            - example.com
```

> Test against the Let's Encrypt **staging** endpoint first. Production rate
> limits are hard and account-wide — 5 failed validations per hostname per
> hour, 50 certificates per registered domain per week — and tripping them
> locks out issuance for the whole domain, not just the name you were testing.

## Configuration

| Value | Default | Description |
| --- | --- | --- |
| `groupName` | _(required)_ | Aggregated API group; a domain you control |
| `credentials.secretName` | `allinkl-api-credentials` | Secret holding the KAS credentials |
| `credentials.userKey` / `.passwordKey` | `user` / `password` | Keys within that Secret |
| `certManager.namespace` | `cert-manager` | Namespace cert-manager runs in |
| `certManager.serviceAccountName` | `cert-manager` | ServiceAccount allowed to call this webhook |
| `image.repository` | `ghcr.io/wenisch-tech/cert-manager-webhook-allinkl` | |
| `securePort` | `8443` | Container port; the Service maps 443 onto it |
| `logLevel` | `2` | klog verbosity |

## How it works

### The KAS API is SOAP in shape only

There is exactly one operation, `KasApi`, whose single `<Params>` element
carries a **JSON** document:

```json
{"KasUser": "...", "KasAuthType": "plain", "KasAuthData": "...",
 "KasRequestType": "add_dns_settings", "KasRequestParams": {...}}
```

So there is no WSDL worth generating code from and no SOAP dependency — the
envelope is a literal template and `encoding/json` does the real work. Only
responses need XML handling, because they arrive as SOAP-encoded `Map` and
`Array` structures (`<item><key/><value/></item>`) rather than anything flat.

### Flood protection is handled, not ignored

KAS rate-limits aggressively and signals it two ways: a `flood_protection`
fault when you are too fast, and a `KasFloodDelay` on every *successful*
response saying how long to wait before the next call.

The client honours both — sleeping out the advertised delay proactively and
retrying (bounded) on the fault. Ignoring `KasFloodDelay` is what makes naive
KAS clients look flaky, because a `Present` immediately followed by `CleanUp`
trips the limit on its own.

### Idempotency

KAS *appends* records rather than upserting, so `Present` checks for an
existing matching TXT record before creating one. Without that, cert-manager
retries would pile up duplicates that `CleanUp` then only partly removes.

## Development

```bash
make test     # go test ./...
make vet
make build
make lint-chart
```

The module lives under `src/`, mirroring the layout of other Go projects in
this organisation. Zone/name splitting and SOAP-map decoding — the two genuinely
fiddly parts — are pinned by unit tests.

## Status

Written for a homelab and used in production there. It has **not** yet been
run against cert-manager's DNS01 conformance suite, which upstream requires
before listing a webhook; that needs a real zone and credentials in CI.

Issues and PRs welcome.
