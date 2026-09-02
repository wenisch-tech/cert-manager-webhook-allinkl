# cert-manager-webhook-allinkl

A [cert-manager](https://cert-manager.io) ACME **DNS-01 solver webhook** for
domains hosted at **All-Inkl** ([all-inkl.com](https://all-inkl.com/)), driving
their KAS API.

cert-manager ships no in-tree provider for All-Inkl, and no community webhook
existed. This fills that gap, which unlocks two things HTTP-01 cannot do:

- **Wildcard certificates** (`*.example.com`) — only ever issuable over DNS-01.
- **Certificates for names that never resolve publicly**, such as LAN-only
  hostnames. DNS-01 validates a `_acme-challenge` TXT record; it never looks at
  the A record of the name being certified, so internal addresses stay out of
  public DNS.

## Install

The chart is published as an OCI artifact, so there is no chart repo to add:

```bash
helm install cert-manager-webhook-allinkl \
  oci://ghcr.io/wenisch-tech/helm-charts/cert-manager-webhook-allinkl \
  --namespace cert-manager \
  --set groupName=acme.example.com
```

`groupName` must be a domain **you** control. It becomes the aggregated API
group this webhook serves, and it must match `groupName` in your issuer. The
chart refuses to render without it.

### Credentials

The chart deliberately does not manage this Secret. KAS credentials control
every DNS record, mailbox and database in the All-Inkl account — far more than
this webhook needs — so they do not belong in a values file or in git.

#### Getting them

1. Sign in to KAS at [kas.all-inkl.com](https://kas.all-inkl.com).
2. Enable the API under **Tools -> API** and set an API password there. The
   same page restricts which source IPs may call the API; if you use it, the
   allowed address is your cluster's outbound NAT address, not a pod IP.
3. Prefer a **sub-account** (KAS: *Unteraccount*) over the main login, so the
   credential can be revoked on its own without locking you out of hosting.
   The sub-account must be able to manage DNS for the zone you will certify.
4. `KasUser` is that KAS login (the main account looks like `wXXXXXXX`);
   `KasAuthData` is its password. The webhook authenticates with
   `KasAuthType: plain` on each call and creates no sessions.

> **The account must own the zone.** A KAS sub-account only sees the domains
> assigned to it, and DNS records belong to whichever account holds the
> domain. A fresh sub-account has none, so every call returns
> `zone_not_found` — with valid credentials and an HTTP 200, which makes it
> look like a bug in this webhook rather than a permissions problem. Check
> with `get_domains` (below): if it comes back empty, that account cannot
> solve challenges for your zone no matter what `zoneName` you set, and you
> need the account the domain actually lives under.

#### Verify before deploying

This is exactly the call the webhook makes, so if it returns your zone the
credentials and permissions are right. Doing this first turns a silent
"challenge stuck pending" into an immediate, readable answer:

```bash
curl -sS https://kasapi.kasserver.com/soap/KasApi.php \
  -H 'Content-Type: text/xml; charset=utf-8' \
  -H 'SOAPAction: urn:xmethodsKasApi#KasApi' \
  --data-binary @- <<'XML' | head -40
<?xml version="1.0" encoding="utf-8"?>
<soap-env:Envelope xmlns:soap-env="http://schemas.xmlsoap.org/soap/envelope/">
  <soap-env:Body>
    <ns0:KasApi xmlns:ns0="urn:xmethodsKasApi">
      <Params>{"KasUser":"wXXXXXXX","KasAuthType":"plain","KasAuthData":"YOUR_PASSWORD","KasRequestType":"get_dns_settings","KasRequestParams":{"zone_host":"example.com."}}</Params>
    </ns0:KasApi>
  </soap-env:Body>
</soap-env:Envelope>
XML
```

A `ReturnString` of `TRUE` plus a list of records means you are good.

Two faults are worth recognising:

- `flood_protection` — you called too fast. Wait a few seconds and retry; the
  webhook handles this itself, sleeping out the delay KAS reports.
- `zone_not_found` — the credentials are *valid* but that account does not
  hold the zone. Swap `get_dns_settings` for `get_domains` (empty
  `KasRequestParams`) to see what the account can actually reach.

#### Create the Secret

```bash
kubectl create secret generic allinkl-api-credentials \
  --namespace cert-manager \
  --from-literal=user='<KAS_USER>' \
  --from-literal=password='<KAS_PASSWORD>'
```

For a `ClusterIssuer`, the Secret must live in cert-manager's
`--cluster-resource-namespace` (`cert-manager` by default). The chart's Role
grants access to exactly this Secret name, so keep the name or set
`credentials.secretName` to match.

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

Runs in production in the homelab it was written for, and is verified end to
end against the real KAS API and Let's Encrypt staging: cert-manager calls the
webhook, the `_acme-challenge` TXT record lands in the zone, Let's Encrypt
validates it, a certificate is issued, and `CleanUp` removes the record again.
Flood protection was hit repeatedly during that run and handled by the
client's backoff.

What has **not** been run is cert-manager's [DNS01 conformance
suite](https://github.com/cert-manager/webhook-example) — the upstream
harness that drives `Present`/`CleanUp` directly and asserts the result with
its own DNS queries, rather than going through ACME. It needs a real zone plus
credentials available to CI. Upstream recommends every webhook pass it; it is
not a gate for anything, because cert-manager deliberately keeps no
approved-provider list (DNS providers were moved out-of-tree precisely so
they would not have to).

Issues and PRs welcome.
