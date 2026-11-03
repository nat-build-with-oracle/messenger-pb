// Passkeys: WebAuthn credentials (Touch ID / Face ID through iCloud Keychain, or a hardware key)
// that sign a superuser or a users record in without a password. No API rules: nobody reads or
// writes the collection directly; every change goes through /api/passkey/* (passkey.go), where the
// owner is always the signed-in record.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		c := core.NewBaseCollection("passkeys")
		c.Fields.Add(
			&core.SelectField{Name: "owner_col", Required: true, MaxSelect: 1, Values: []string{"_superusers", "users"}},
			&core.TextField{Name: "owner_id", Required: true, Max: 30},
			&core.TextField{Name: "cred_id", Required: true, Max: 1400}, // base64url of the credential id
			&core.TextField{Name: "name", Max: 100},
			&core.JSONField{Name: "credential", Required: true, MaxSize: 64 << 10},
			&core.DateField{Name: "last_used"},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		c.AddIndex("idx_passkeys_cred", true, "cred_id", "")
		c.AddIndex("idx_passkeys_owner", false, "owner_col, owner_id", "")
		return app.Save(c)
	}, func(app core.App) error {
		if c, err := app.FindCollectionByNameOrId("passkeys"); err == nil {
			return app.Delete(c)
		}
		return nil
	})
}
