// threads.folder: Messenger's folder for the chat — inbox, pending (message requests:
// someone you are not connected with wrote first, and the chat waits outside the inbox),
// or community. From msys threads.folder_name; E2EE chats are always inbox.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("threads")
		if err != nil {
			return err
		}
		c.Fields.Add(&core.TextField{Name: "folder", Max: 40})
		c.AddIndex("idx_threads_folder", false, "folder, last_ts", "")
		return app.Save(c)
	}, nil)
}
