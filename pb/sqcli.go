// `messenger-pb sq …` — the agent's side of send-queue. It talks to the running server over
// HTTP as the agent user, so every rule is enforced server-side, exactly like the original
// `bun sq.ts`. `messenger-pb sq user …` is the one in-process operator command (it creates
// the agent / reviewer accounts and never touches drafts).
//
//	SQ_API=http://127.0.0.1:8092  SQ_AGENT_EMAIL=agent@m5.local  SQ_AGENT_PASSWORD=…
//	messenger-pb sq read '#a=c:KEY' [--last 30]           # one chat as text, oldest first (sqread.go)
//	messenger-pb sq add --thread '#a=c:KEY' --file msg.txt  # chat name filled from the archive
//	messenger-pb sq add --platform Messenger --chat "Exact Chat Name" --file msg.txt [--thread KEY] [--note …]
//	messenger-pb sq get <id>
//	messenger-pb sq revise <id> --file msg.txt --comment "what changed"
//	messenger-pb sq ok <id>                      # exit 0 only if the current rev is approved
//	messenger-pb sq sent <id> --proof "…" [--img shot.png]
//	messenger-pb sq failed <id> --proof "why"
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

type sqClient struct{ base, token string }

func newSQClient() (*sqClient, error) {
	c := &sqClient{base: strings.TrimRight(envOr("SQ_API", "http://127.0.0.1:8092"), "/")}
	email, pass := os.Getenv("SQ_AGENT_EMAIL"), os.Getenv("SQ_AGENT_PASSWORD")
	if email == "" || pass == "" {
		return nil, fmt.Errorf("set SQ_AGENT_EMAIL and SQ_AGENT_PASSWORD (the agent user, never a superuser)")
	}
	body, _ := json.Marshal(map[string]string{"identity": email, "password": pass})
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do("POST", "/api/collections/users/auth-with-password", "application/json", bytes.NewReader(body), &out); err != nil {
		return nil, fmt.Errorf("agent login: %w", err)
	}
	c.token = out.Token
	return c, nil
}

func (c *sqClient) do(method, path, ctype string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("content-type", ctype)
	}
	if c.token != "" {
		req.Header.Set("authorization", c.token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 { // 204 No Content leaves out untouched
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *sqClient) post(path string, v any) (map[string]any, error) {
	b, _ := json.Marshal(v)
	var out map[string]any
	return out, c.do("POST", path, "application/json", bytes.NewReader(b), &out)
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func readFileArg(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--file is required")
	}
	b, err := os.ReadFile(path)
	return strings.TrimRight(string(b), "\n"), err
}

func registerSQCommands(app *pocketbase.PocketBase) {
	sq := &cobra.Command{Use: "sq", Short: "send-queue: queue drafts, check approval, record proof (agent side)"}

	var platform, chat, file, thread, note, srcNote, srcURL, batch, part, comment, proof, img string

	add := &cobra.Command{Use: "add", Short: "queue a draft (pending)", RunE: func(cmd *cobra.Command, args []string) error {
		msg, err := readFileArg(file)
		if err != nil {
			return err
		}
		c, err := newSQClient()
		if err != nil {
			return err
		}
		// --thread takes a key or a page link; with no --chat, the archive's name for it is used, so a
		// draft is tied to the chat it answers, not to a name that may belong to another chat.
		if thread != "" {
			p, k, err := parseChatRef(thread, map[bool]string{true: platform}[cmd.Flags().Changed("platform")])
			if err != nil {
				return err
			}
			platform, thread = p, k
			if chat == "" {
				if chat = c.chatName(p, k); chat == "" {
					return fmt.Errorf("no chat with key %s in the archive; give --chat", k)
				}
			}
		}
		out, err := c.post("/api/sq/sends", map[string]string{"platform": platform, "chat": chat, "message": msg,
			"threadKey": thread, "note": note, "sourceNote": srcNote, "sourceUrl": srcURL, "batch": batch, "part": part})
		if err != nil {
			return err
		}
		printJSON(out)
		return nil
	}}
	add.Flags().StringVar(&platform, "platform", "Messenger", "Messenger | LINE")
	add.Flags().StringVar(&chat, "chat", "", "exact chat name")
	add.Flags().StringVar(&file, "file", "", "file holding the message text")
	add.Flags().StringVar(&thread, "thread", "", "thread key or page link (#a=c:KEY); fills --chat from the archive")
	add.Flags().StringVar(&note, "note", "", "context for the reviewer")
	add.Flags().StringVar(&srcNote, "source-note", "", "what raw data the draft was built from")
	add.Flags().StringVar(&srcURL, "source-url", "", "link to that data")
	add.Flags().StringVar(&batch, "batch", "", "batch id")
	add.Flags().StringVar(&part, "part", "", "e.g. 1/2")

	get := &cobra.Command{Use: "get <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newSQClient()
		if err != nil {
			return err
		}
		var rec, evs map[string]any
		if err := c.do("GET", "/api/collections/sends/records/"+args[0], "", nil, &rec); err != nil {
			return err
		}
		if err := c.do("GET", "/api/collections/send_events/records?perPage=200&sort=created&filter="+
			strings.ReplaceAll(fmt.Sprintf("send='%s'", args[0]), " ", "%20"), "", nil, &evs); err != nil {
			return err
		}
		rec["events"] = evs["items"]
		printJSON(rec)
		return nil
	}}

	revise := &cobra.Command{Use: "revise <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		msg, err := readFileArg(file)
		if err != nil {
			return err
		}
		c, err := newSQClient()
		if err != nil {
			return err
		}
		out, err := c.post("/api/sq/sends/"+args[0]+"/revise", map[string]string{"message": msg, "comment": comment})
		if err != nil {
			return err
		}
		printJSON(out)
		return nil
	}}
	revise.Flags().StringVar(&file, "file", "", "file holding the new text")
	revise.Flags().StringVar(&comment, "comment", "", "what changed")

	ok := &cobra.Command{Use: "ok <id>", Short: "exit 0 only if the current revision is approved",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newSQClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/sq/sends/"+args[0]+"/ok", "", nil, &out); err != nil {
				return err // 409 when not approved → non-zero exit
			}
			printJSON(out)
			return nil
		}}

	finish := func(outcome string) *cobra.Command {
		cmd := &cobra.Command{Use: outcome + " <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newSQClient()
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			_ = w.WriteField("proof", proof)
			if img != "" {
				f, err := os.Open(img)
				if err != nil {
					return err
				}
				part, _ := w.CreateFormFile("proof_img", filepath.Base(img))
				_, _ = io.Copy(part, f)
				f.Close()
			}
			w.Close()
			var out map[string]any
			if err := c.do("POST", "/api/sq/sends/"+args[0]+"/"+outcome, w.FormDataContentType(), &buf, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		}}
		cmd.Flags().StringVar(&proof, "proof", "", "what happened (required)")
		if outcome == "sent" {
			cmd.Flags().StringVar(&img, "img", "", "screenshot of the sent message")
		}
		return cmd
	}

	var role string
	user := &cobra.Command{Use: "user <email> <password>", Short: "create or update an agent/reviewer account (operator, in-process)",
		Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if role != "agent" && role != "reviewer" && role != "sender" && role != "viewer" {
				return fmt.Errorf("--role must be agent, reviewer, sender or viewer")
			}
			col, err := app.FindCollectionByNameOrId("users")
			if err != nil {
				return err
			}
			rec, err := app.FindAuthRecordByEmail(col, args[0])
			if err != nil {
				rec = core.NewRecord(col)
				rec.SetEmail(args[0])
			}
			rec.SetPassword(args[1])
			rec.SetVerified(true)
			rec.Set("role", role)
			if err := app.Save(rec); err != nil {
				return err
			}
			fmt.Printf("saved %s as %s (users/%s)\n", args[0], role, rec.Id)
			return nil
		}}
	user.Flags().StringVar(&role, "role", "agent", "agent | reviewer | sender | viewer")

	var last int
	read := &cobra.Command{Use: "read <link|key>", Short: "print one chat as text, oldest first (input for a draft)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, k, err := parseChatRef(args[0], map[bool]string{true: platform}[cmd.Flags().Changed("platform")])
			if err != nil {
				return err
			}
			c, err := newSQClient()
			if err != nil {
				return err
			}
			if last <= 0 || last > 500 {
				last = 30
			}
			lines, total, err := c.readChat(p, k, last)
			if err != nil {
				return err
			}
			name := c.chatName(p, k)
			if name == "" {
				name = "(unnamed chat)"
			}
			fmt.Print(formatChat(p, k, name, lines, total))
			return nil
		}}
	read.Flags().IntVar(&last, "last", 30, "how many of the newest messages (max 500)")
	read.Flags().StringVar(&platform, "platform", "Messenger", "Messenger | LINE, when the key carries no prefix")

	// The reply desk's side of draft requests (sqdraft.go): claim the next one, close it.
	next := &cobra.Command{Use: "next-request", Short: "claim the oldest open draft request; prints it, exit 3 when there is none",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newSQClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("POST", "/api/sq/draft-requests/next", "application/json", strings.NewReader("{}"), &out); err != nil {
				return err
			}
			if out == nil || out["id"] == nil {
				os.Exit(3)
			}
			printJSON(map[string]any{"id": out["id"], "link": out["link"], "chat": out["chat"], "platform": out["platform"]})
			return nil
		}}
	var rqStatus, rqSend, rqNote string
	closeRq := &cobra.Command{Use: "close-request <id>", Short: "close a claimed draft request (done | skipped | failed)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newSQClient()
			if err != nil {
				return err
			}
			out, err := c.post("/api/sq/draft-requests/"+args[0]+"/close", map[string]string{"status": rqStatus, "send": rqSend, "note": rqNote})
			if err != nil {
				return err
			}
			printJSON(map[string]any{"id": out["id"], "status": out["status"], "send": out["send"]})
			return nil
		}}
	closeRq.Flags().StringVar(&rqStatus, "status", "done", "done | skipped | failed")
	closeRq.Flags().StringVar(&rqSend, "send", "", "the draft it queued")
	closeRq.Flags().StringVar(&rqNote, "note", "", "why, for skipped / failed")

	sq.AddCommand(add, read, get, revise, ok, finish("sent"), finish("failed"), user, next, closeRq)
	app.RootCmd.AddCommand(sq)
}
