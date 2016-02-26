// Program rssnotify looks for changes to RSS/Atom feeds and notifies
// you via e-mail.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"net/smtp"
	"os"
	"os/user"
	"path"
	"time"

	"github.com/SlyMarbo/rss"
	"github.com/boltdb/bolt"
)

type update struct {
	Title string
	Items []*rss.Item
}

// emailTemplate is the message template we'll use for generating the
// e-mail message.
var emailTemplate = template.Must(template.New("email").Funcs(template.FuncMap{
	"formattedDate": func(t time.Time) string {
		return t.Format("2006-02-01 15:04:05")
	},
}).Parse(`To: {{.To}}
From: {{.From}}
Subject: {{.Subject}}

{{range .Updates}}
* {{.Title}}
{{range .Items}}
{{.Date | formattedDate}} - {{.Title}}
{{.Link}}

{{end}}

{{end}}
`))

// Our command line arguments.
var (
	// flags is a struct to store flag values. We do this so we can easily
	// pass the information to the template engine.
	flags struct {
		To      string
		From    string
		Subject string
		Addr    string
		Updates []*update
	}

	// Additional configs for local files.
	feedsFile string
	dbFile    string
)

func init() {
	// Set the defailt to/from to user@host.
	u := "none"
	if user, err := user.Current(); err == nil {
		u = user.Username
	}
	if host, err := os.Hostname(); err == nil {
		u = u + "@" + host
	}

	flag.StringVar(&flags.To, "to", u, "the name to send the e-mails as.")
	flag.StringVar(&flags.From, "from", u, "the name to send the e-mails to.")
	flag.StringVar(&flags.Subject, "subject", "[rssnotify] Updated Feeds",
		"the subject of the e-mails.")
	flag.StringVar(&flags.Addr, "addr", "localhost:smtp",
		"the SMTP server to use to send the e-mail.")

	flag.StringVar(&feedsFile, "feeds", os.ExpandEnv("$HOME/.config/rssnotify/feeds"),
		"besides using command line arguments, also get a feed list from this file.")
	flag.StringVar(&dbFile, "db", os.ExpandEnv("$HOME/.local/share/rssnotify/db"),
		"the location of the database where feed history is stored.")
}

func main() {
	flag.Parse()

	// Open up our bolt database.
	err := os.MkdirAll(path.Dir(dbFile), 0700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to make directory for db: %v\n", err)
		os.Exit(1)
	}
	db, err := bolt.Open(dbFile, 0600, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to make db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get our feed list.
	feeds := parseFeedsFile(nil)
	feeds = append(feeds, flag.Args()...)

	// Check for any updates.
	err = db.Update(func(tx *bolt.Tx) error {
		// Loop through the feed list.
		for _, feed := range feeds {
			// Get the feed data.
			f, err := rss.Fetch(feed)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed fetching feed '%v': %v\n", feed, err)
				continue
			}

			// Create the bucket for this feed.
			bucket, err := tx.CreateBucketIfNotExists([]byte(f.UpdateURL))
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed creating bucket '%v': %v\n", f.UpdateURL, err)
				continue
			}

			// Check for updates to the feeds.
			upd := &update{
				Title: f.Title,
			}
			for _, item := range f.Items {
				// Check to see if we already have it.
				if bucket.Get([]byte(item.ID)) != nil {
					continue
				}

				// Add the item to our list and mark it read.
				upd.Items = append(upd.Items, item)
				err = bucket.Put([]byte(item.ID), []byte("1"))
			}

			// Check to see if we added any and include it in our updates.
			if len(upd.Items) > 0 {
				flags.Updates = append(flags.Updates, upd)
			}
		}
		return nil
	})
	if err != nil {
		// We don't exit here because we may have gotten some.
		fmt.Fprintf(os.Stderr, "failed with db txn: %v\n", err)
	}

	if len(flags.Updates) < 1 {
		// Nothing was updated.
		os.Exit(0)
	}

	// Execute the e-mail template.
	buf := &bytes.Buffer{}
	if err := emailTemplate.Execute(buf, &flags); err != nil {
		fmt.Fprintf(os.Stderr, "failed executing template: %v\n", err)
		os.Exit(1)
	}

	// TODO allow for authentication.
	// TODO allow multiple To's.
	// Send the message.
	err = smtp.SendMail(flags.Addr, nil, flags.From, []string{flags.To}, buf.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed sending message: %v\n", err)
		os.Exit(1)
	}
}

func parseFeedsFile(feeds []string) []string {
	// Don't parse if it doesn't exists. We do this here because it's
	// not an error we want to report.
	if _, err := os.Stat(feedsFile); os.IsNotExist(err) {
		return nil
	}

	f, err := os.Open(feedsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed opening feedsFile '%v': %v", feedsFile, err)
		os.Exit(1)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		feeds = append(feeds, s.Text())
	}
	if err := s.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed parsing feedsFile '%v': %v", feedsFile, err)
		os.Exit(1)
	}
	return feeds
}
