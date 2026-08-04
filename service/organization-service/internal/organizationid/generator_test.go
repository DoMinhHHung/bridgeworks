package organizationid

import "testing"

func TestUUIDV7Generator(t *testing.T) {
	id, err := (UUIDV7Generator{}).New()
	if err != nil { t.Fatal(err) }
	if id.Version() != 7 { t.Fatalf("version=%d", id.Version()) }
}
