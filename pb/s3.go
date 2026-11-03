// S3 for PocketBase's own file storage and backups (a RustFS server over tailscale).
//
//	S3_ACCESS_KEY=… S3_SECRET=… ./messenger-pb s3 set     # files + backups, daily backup
//	./messenger-pb s3 status
//
// The key comes from the environment, never a flag, so it stays out of ps and shell history.
// The settings are read once at start: restart the server after `s3 set`.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
)

func registerS3(app *pocketbase.PocketBase) {
	s3 := &cobra.Command{Use: "s3", Short: "PocketBase files storage and backups on S3"}

	var endpoint, bucket, region, cron string
	var keep int
	var files, backups bool
	set := &cobra.Command{Use: "set", Short: "point files storage and/or backups at S3 (key from S3_ACCESS_KEY / S3_SECRET)",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, secret := os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET")
			if key == "" || secret == "" {
				return fmt.Errorf("set S3_ACCESS_KEY and S3_SECRET in the environment")
			}
			st := app.Settings()
			if files {
				st.S3.Enabled, st.S3.Endpoint, st.S3.Bucket, st.S3.Region = true, endpoint, bucket, region
				st.S3.AccessKey, st.S3.Secret, st.S3.ForcePathStyle = key, secret, true
			}
			if backups {
				b := &st.Backups.S3
				b.Enabled, b.Endpoint, b.Bucket, b.Region = true, endpoint, bucket, region
				b.AccessKey, b.Secret, b.ForcePathStyle = key, secret, true
				st.Backups.Cron, st.Backups.CronMaxKeep = cron, keep
			}
			if err := app.Save(st); err != nil {
				return err
			}
			fmt.Println("saved; restart the server to use it")
			return nil
		}}
	// the S3 host's tailnet IP, not its name: dns.go asks public DNS, which cannot resolve *.ts.net
	set.Flags().StringVar(&endpoint, "endpoint", "http://100.64.0.3:9200", "S3 endpoint (RustFS)")
	set.Flags().StringVar(&bucket, "bucket", "messenger-archive", "bucket")
	set.Flags().StringVar(&region, "region", "rustfs", "region")
	set.Flags().BoolVar(&files, "files", true, "use S3 for files storage")
	set.Flags().BoolVar(&backups, "backups", true, "use S3 for backups")
	set.Flags().StringVar(&cron, "backup-cron", "0 3 * * *", "automatic backup schedule (server time zone UTC)")
	set.Flags().IntVar(&keep, "backup-keep", 14, "automatic backups to keep")

	status := &cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		st := app.Settings()
		fmt.Printf("files:   enabled=%v %s/%s region=%s key=%s\n", st.S3.Enabled, st.S3.Endpoint, st.S3.Bucket, st.S3.Region, st.S3.AccessKey)
		b := st.Backups.S3
		fmt.Printf("backups: enabled=%v %s/%s region=%s key=%s cron=%q keep=%d\n", b.Enabled, b.Endpoint, b.Bucket, b.Region, b.AccessKey, st.Backups.Cron, st.Backups.CronMaxKeep)
		return nil
	}}

	test := &cobra.Command{Use: "test", Short: "write, read and delete a probe through files storage and backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, which := range []string{"files", "backups"} {
				fsys, err := app.NewFilesystem()
				if which == "backups" {
					fsys, err = app.NewBackupsFilesystem()
				}
				if err != nil {
					return fmt.Errorf("%s: %w", which, err)
				}
				key := "s3-probe-" + which + ".txt"
				err = fsys.Upload([]byte("ok"), key)
				if err == nil {
					var ok bool
					ok, err = fsys.Exists(key)
					if err == nil && !ok {
						err = fmt.Errorf("probe not found after upload")
					}
				}
				if err == nil {
					err = fsys.Delete(key)
				}
				fsys.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", which, err)
				}
				fmt.Printf("%s: write, read, delete ok\n", which)
			}
			return nil
		}}

	backup := &cobra.Command{Use: "backup [name.zip]", Short: "make a backup now (to S3 when backups use S3)", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if err := app.CreateBackup(context.Background(), name); err != nil {
				return err
			}
			fsys, err := app.NewBackupsFilesystem()
			if err != nil {
				return err
			}
			defer fsys.Close()
			list, err := fsys.List("")
			if err != nil {
				return err
			}
			for _, f := range list {
				fmt.Printf("%s  %d bytes\n", f.Key, f.Size)
			}
			return nil
		}}

	s3.AddCommand(set, status, test, backup)
	app.RootCmd.AddCommand(s3)
}
