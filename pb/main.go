// messenger-pb — the Messenger archive as a PocketBase app.
//
//	./messenger-pb migrate up                               # schema (migrations/)
//	./messenger-pb superuser upsert demo@m5.local admindemo # demo admin (local only)
//	./messenger-pb import --fts fts.db --msys msys.db       # bulk load, hooks muted
//	./messenger-pb serve --http 127.0.0.1:8090              # API + admin UI + sync + MQTT hooks
//
// Every message record created while serving (phone sync, REST API, admin UI)
// is published to MQTT by hooks.go.
package main

import (
	"log"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	_ "messenger-pb/migrations"
)

func main() {
	app := pocketbase.New()

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: false, // schema changes only through migrations/ — no admin-UI drift
	})

	registerMQTTHooks(app)
	registerImport(app)
	registerSync(app)
	registerSendQueue(app)
	registerSQCommands(app)
	registerChatMedia(app)
	registerLine(app)
	registerSQWeb(app)
	registerSender(app)
	registerForward(app)
	registerAliases(app)
	registerApps(app)
	registerCronLog(app)
	registerDraftRequests(app)
	registerS3(app)
	registerLoginLink(app)
	registerPeople(app)
	registerAvatar(app)
	registerPasskeys(app)
	registerAdminPasskey(app)

	if err := app.Start(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
