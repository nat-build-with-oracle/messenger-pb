// MQTT hooks: every created message record is published as JSON to
// <prefix>/messages/<thread_key>; thread upserts go to <prefix>/threads/<key>.
// Bulk import mutes per-record publishing and sends one summary instead.
package main

import (
	"encoding/json"
	"log"
	"os"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/pocketbase/pocketbase/core"
)

var (
	mqttClient mqtt.Client
	muted      atomic.Bool // set during bulk import
	topicRoot  = envOr("MQTT_PREFIX", "messenger")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// connectMQTT is opt-in: with no MQTT_URL nothing is published. A default
// broker address is dangerous on a shared machine — 127.0.0.1:1883/1884 here are
// the fleet's buses, and private message text must never land there by accident.
func connectMQTT() mqtt.Client {
	url := os.Getenv("MQTT_URL")
	if url == "" {
		log.Println("mqtt: off (set MQTT_URL to publish)")
		return nil
	}
	opts := mqtt.NewClientOptions().
		AddBroker(url).
		SetClientID("messenger-pb-" + time.Now().Format("150405")).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(3 * time.Second).
		SetOnConnectHandler(func(mqtt.Client) { log.Println("mqtt: connected") }).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) { log.Println("mqtt: lost:", err) })
	if u := os.Getenv("MQTT_USER"); u != "" {
		opts.SetUsername(u).SetPassword(os.Getenv("MQTT_PASS"))
	}
	c := mqtt.NewClient(opts)
	c.Connect() // ConnectRetry: returns immediately, keeps trying in the background
	return c
}

func publish(topic string, v any) {
	if mqttClient == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	t := mqttClient.Publish(topicRoot+"/"+topic, 1, false, b)
	go func() {
		if t.WaitTimeout(5*time.Second) && t.Error() != nil {
			log.Println("mqtt publish:", t.Error())
		}
	}()
}

func registerMQTTHooks(app core.App) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if c := connectMQTT(); c != nil {
			mqttClient = c
		}
		return e.Next()
	})

	app.OnRecordAfterCreateSuccess("messages").BindFunc(func(e *core.RecordEvent) error {
		if !muted.Load() {
			r := e.Record
			publish("messages/"+r.GetString("thread_key"), map[string]any{
				"id":          r.Id,
				"mid":         r.GetString("mid"),
				"thread_key":  r.GetString("thread_key"),
				"thread_name": threadName(e.App, r.GetString("thread")),
				"ts":          r.GetInt("ts"),
				"sender":      r.GetString("sender"),
				"sender_name": r.GetString("sender_name"),
				"text":        r.GetString("text"),
				"kind":        r.GetString("kind"),
				"source":      r.GetString("source"),
				"atts":        r.Get("atts"),
			})
		}
		return e.Next()
	})

	app.OnRecordAfterCreateSuccess("threads").BindFunc(func(e *core.RecordEvent) error {
		if !muted.Load() {
			publish("threads/"+e.Record.GetString("key"), e.Record.PublicExport())
		}
		return e.Next()
	})
}

func threadName(app core.App, threadID string) string {
	if threadID == "" {
		return ""
	}
	t, err := app.FindRecordById("threads", threadID)
	if err != nil {
		return ""
	}
	return t.GetString("name")
}
