package cryptutil

import (
	"testing"

	"github.com/google/uuid"
)

func TestForTable_KnownTables(t *testing.T) {
	cases := []struct {
		table string
		ref   string
	}{
		{TableCryptoAddresses, "0xdead"},
		{TableCustodyWallets, "TA0000000000000000000000000000a002"},
		{TableTwoFactor, uuid.New().String()},
	}
	for _, c := range cases {
		aad, err := ForTable(c.table, c.ref)
		if err != nil {
			t.Errorf("ForTable(%q, %q): error inesperado: %v", c.table, c.ref, err)
		}
		if aad.Table == "" || aad.Column == "" || aad.Ref == "" {
			t.Errorf("ForTable(%q, %q): AAD incompleto: %+v", c.table, c.ref, aad)
		}
	}
}

func TestForTable_UnknownTable_Rejected(t *testing.T) {
	_, err := ForTable("unknown_table", "ref")
	if err == nil {
		t.Fatal("se esperaba error para tabla desconocida")
	}
}
