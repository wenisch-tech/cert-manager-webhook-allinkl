package kas

import (
	"encoding/xml"
	"testing"
)

// A trimmed but structurally faithful get_dns_settings response: SOAP-encoded
// Maps nested inside an Array, which is the shape decodeNode has to survive.
const sampleResponse = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"
                   xmlns:ns2="http://xml.apache.org/xml-soap"
                   xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <SOAP-ENV:Body>
    <ns1:KasApiResponse xmlns:ns1="urn:xmethodsKasApi">
      <return xsi:type="ns2:Map">
        <item><key xsi:type="xsd:string">Request</key><value xsi:type="ns2:Map">
          <item><key xsi:type="xsd:string">KasRequestType</key><value xsi:type="xsd:string">get_dns_settings</value></item>
        </value></item>
        <item><key xsi:type="xsd:string">Response</key><value xsi:type="ns2:Map">
          <item><key xsi:type="xsd:string">ReturnString</key><value xsi:type="xsd:string">TRUE</value></item>
          <item><key xsi:type="xsd:string">KasFloodDelay</key><value xsi:type="xsd:float">2</value></item>
          <item><key xsi:type="xsd:string">ReturnInfo</key><value xsi:type="ns2:Array">
            <item xsi:type="ns2:Map">
              <item><key xsi:type="xsd:string">record_id</key><value xsi:type="xsd:string">1234</value></item>
              <item><key xsi:type="xsd:string">record_name</key><value xsi:type="xsd:string">_acme-challenge.lan</value></item>
              <item><key xsi:type="xsd:string">record_type</key><value xsi:type="xsd:string">TXT</value></item>
              <item><key xsi:type="xsd:string">record_data</key><value xsi:type="xsd:string">token-one</value></item>
              <item><key xsi:type="xsd:string">record_zone</key><value xsi:type="xsd:string">example.com</value></item>
            </item>
            <item xsi:type="ns2:Map">
              <item><key xsi:type="xsd:string">record_id</key><value xsi:type="xsd:string">5678</value></item>
              <item><key xsi:type="xsd:string">record_name</key><value xsi:type="xsd:string">www</value></item>
              <item><key xsi:type="xsd:string">record_type</key><value xsi:type="xsd:string">A</value></item>
              <item><key xsi:type="xsd:string">record_data</key><value xsi:type="xsd:string">203.0.113.10</value></item>
              <item><key xsi:type="xsd:string">record_zone</key><value xsi:type="xsd:string">example.com</value></item>
            </item>
          </value></item>
        </value></item>
      </return>
    </ns1:KasApiResponse>
  </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

func TestDecodeGetDNSSettings(t *testing.T) {
	var env soapEnvelope
	if err := xml.Unmarshal([]byte(sampleResponse), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Body.Fault != nil {
		t.Fatal("unexpected fault")
	}
	if len(env.Body.Content) == 0 {
		t.Fatal("no body content")
	}

	decoded, ok := decodeNode(env.Body.Content[0]).(map[string]any)
	if !ok {
		t.Fatalf("decoded to %T, want map", decodeNode(env.Body.Content[0]))
	}
	resp, ok := decoded["Response"].(map[string]any)
	if !ok {
		t.Fatalf("Response is %T, want map", decoded["Response"])
	}
	if resp["ReturnString"] != "TRUE" {
		t.Errorf("ReturnString = %v, want TRUE", resp["ReturnString"])
	}
	if d, ok := asFloat(resp["KasFloodDelay"]); !ok || d != 2 {
		t.Errorf("KasFloodDelay = %v (ok=%v), want 2", d, ok)
	}

	entries, ok := resp["ReturnInfo"].([]any)
	if !ok {
		t.Fatalf("ReturnInfo is %T, want slice", resp["ReturnInfo"])
	}
	if len(entries) != 2 {
		t.Fatalf("got %d records, want 2", len(entries))
	}
	first, _ := entries[0].(map[string]any)
	if str(first["record_id"]) != "1234" || str(first["record_type"]) != "TXT" {
		t.Errorf("first record decoded wrong: %#v", first)
	}
	if str(first["record_name"]) != "_acme-challenge.lan" {
		t.Errorf("record_name = %q", str(first["record_name"]))
	}
}

func TestFloodDelayDetection(t *testing.T) {
	if _, ok := floodDelayFrom("some_other_error", "3"); ok {
		t.Error("non-flood fault treated as flood")
	}
	d, ok := floodDelayFrom("flood_protection", "3")
	if !ok || d.Seconds() != 3 {
		t.Errorf("floodDelayFrom = %v (ok=%v), want 3s", d, ok)
	}
	// An unparseable detail must still be treated as flood protection, just
	// with the conservative default delay.
	d, ok = floodDelayFrom("flood_protection", map[string]any{"msg": "slow down"})
	if !ok || d != defaultFloodDelay {
		t.Errorf("floodDelayFrom fallback = %v (ok=%v), want %v", d, ok, defaultFloodDelay)
	}
}
