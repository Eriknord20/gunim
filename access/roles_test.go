package access

import "testing"

func TestEveryRoleHasAName(t *testing.T) {
	for r := RoleGroup; r <= RoleRadioButton; r++ {
		if r.String() == "unknown" {
			t.Errorf("role %d has no name", r)
		}
	}
	for r, want := range map[Role]string{RoleStatus: "status", RoleAlert: "alert", RoleRadioButton: "radio button"} {
		if got := r.String(); got != want {
			t.Errorf("role %d is named %q, want %q", r, got, want)
		}
	}
}
