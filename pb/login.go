// Login links: open the page already signed in, no password typed.
//
//	./messenger-pb login-link viewer@example.com [--for 24h]   # print a link
//	./messenger-pb login-link viewer@example.com --revoke      # every link, login and passkey of that user dies
//
// Only role=agent and role=viewer: a reviewer approves sends, so a reviewer always types a password.
// The token sits after "#", so the browser never sends it to the server (no access log holds it),
// and the page removes it from the address bar as soon as it reads it. It is a PocketBase static
// auth token: it cannot be refreshed, so the login ends when the link expires.
// Anyone who has the link is signed in as that user until then. Send it privately.
package main

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
)

func registerLoginLink(app *pocketbase.PocketBase) {
	var dur time.Duration
	var base string
	var revoke bool
	cmd := &cobra.Command{Use: "login-link <email>", Short: "print a link that signs an agent or viewer in (or --revoke all of them)",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			rec, err := app.FindAuthRecordByEmail("users", args[0])
			if err != nil {
				return fmt.Errorf("no users record %s", args[0])
			}
			if revoke {
				rec.RefreshTokenKey()
				if err := app.Save(rec); err != nil {
					return err
				}
				// a passkey would sign the user in again: it goes too
				rows, err := passkeyRows(app, rec)
				if err != nil {
					return err
				}
				for _, r := range rows {
					if err := app.Delete(r); err != nil {
						return err
					}
				}
				fmt.Printf("revoked: every token of %s is now invalid, %d passkey(s) removed\n", args[0], len(rows))
				return nil
			}
			if role := rec.GetString("role"); role != "agent" && role != "viewer" {
				return fmt.Errorf("%s is role %q: links only for agent or viewer", args[0], role)
			}
			if dur <= 0 || dur > 30*24*time.Hour {
				return fmt.Errorf("--for must be between 1s and 720h")
			}
			tok, err := rec.NewStaticAuthToken(dur)
			if err != nil {
				return err
			}
			fmt.Printf("%s/#login=%s\n", base, tok)
			fmt.Printf("signs in as %s (%s) until %s\n", args[0], rec.GetString("role"), time.Now().Add(dur).Format("02 Jan 15:04 MST"))
			return nil
		}}
	cmd.Flags().DurationVar(&dur, "for", 24*time.Hour, "how long the link works (max 720h)")
	// the phone's tailnet address: the page is served there, see dns.go for why not a name
	cmd.Flags().StringVar(&base, "base", "http://100.64.0.2:8097", "page address")
	cmd.Flags().BoolVar(&revoke, "revoke", false, "invalidate every link and login of this user")
	app.RootCmd.AddCommand(cmd)
}
