package clusters

import (
	"encoding/pem"
	"net/http/httptest"
	"testing"
)

// pemForCert returns the PEM-encoded certificate the httptest TLS server
// presents. Because httptest self-signs, this cert doubles as the CA to trust.
func pemForCert(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	cert := srv.Certificate()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
