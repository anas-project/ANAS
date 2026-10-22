package appcatalog

import "testing"

func TestCredentialDeclarationOnlyAllowsExplicitBuiltinLocalAdmins(t *testing.T) {
	metadata := sampleInput("core-admin").Application.Metadata
	metadata.URL = ""
	metadata.Kind = "admin"
	base := Declaration{Metadata: metadata, URLFrom: "CORE_URL", Access: Access{Mode: "local"}, Visibility: Visibility{Mode: "subjects", AnyOf: []Subject{{Role: "platform_admin"}}}, Credentials: &CredentialReference{LocalAccount: "primary"}}
	if err := ValidateDeclaration(base, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Declaration)
	}{
		{"iam", func(d *Declaration) { d.Access = Access{Mode: "iam", Client: "core"} }},
		{"user", func(d *Declaration) { d.Kind = "user" }},
		{"empty", func(d *Declaration) { d.Credentials = &CredentialReference{} }},
		{"mixed", func(d *Declaration) {
			d.Credentials = &CredentialReference{LocalAccount: "primary", UsernameFrom: "CORE_USER", PasswordFrom: "CORE_PASSWORD"}
		}},
		{"same-source", func(d *Declaration) {
			d.Credentials = &CredentialReference{UsernameFrom: "CORE_PASSWORD", PasswordFrom: "CORE_PASSWORD"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.change(&d)
			if ValidateDeclaration(d, true) == nil {
				t.Fatal("invalid credential declaration accepted")
			}
		})
	}
	if ValidateDeclaration(base, false) == nil {
		t.Fatal("external credential declaration accepted")
	}
}
