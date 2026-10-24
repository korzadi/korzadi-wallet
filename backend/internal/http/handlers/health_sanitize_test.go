package handlers

import "testing"

func TestPublicHealthError_NeverEchoesRawDetail(t *testing.T) {
	raw := `rpc unreachable: Post "https://node.example/v1/SECRETKEY": timeout`
	for _, got := range []string{
		publicHealthError(false, false, raw),
		publicHealthError(true, false, raw),
		publicHealthError(true, true, raw),
	} {
		if got == "" || got == raw {
			t.Fatalf("respuesta pública inesperada: %q", got)
		}
	}
	if publicHealthError(true, true, "") != "" {
		t.Fatal("sin error debe devolver vacío")
	}
}
