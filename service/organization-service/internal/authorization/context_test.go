package authorization

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestActorContextCopiesAndSortsPermissions(t *testing.T) {
	permissions := []string{"organization.read", "membership.manage"}
	actor := NewActorContext(uuid.MustParse("01890f8c-8a7a-7cc0-98c7-9f4b25a29a11"), uuid.MustParse("01890f8c-8a7a-7cc0-98c7-9f4b25a29a12"), uuid.MustParse("01890f8c-8a7a-7cc0-98c7-9f4b25a29a13"), "admin", permissions)
	permissions[0] = "tampered"
	if !actor.HasPermission("organization.read") || actor.HasPermission("tampered") {
		t.Fatal("actor permissions were not copied")
	}
	want := []string{"membership.manage", "organization.read"}
	if got := actor.Permissions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Permissions()=%v want %v", got, want)
	}
}
