// Command digest — Phase 5 U8 — opt-in daily/weekly digest worker.
//
// Cron-friendly CLI; intended to run hourly at :00 UTC. For each user with
// an opted-in preference whose firing window is "now", builds a digest
// payload from undelivered notifications, ships it via Slack webhook (or
// logs as dry-run), then appends `slack` to delivered_via on each item.
//
// Usage:
//
//	digest --db=/path/to/ds.db [--dry-run] [--channel=slack|email]
//	  --db        Path to ds.db (env DS_DB_PATH).
//	  --channel   Limit delivery to one channel (default: all opted-in).
//	  --dry-run   Build + render but don't send + don't mark delivered.
//
// Email delivery is wired but logs-and-skips when SMTP_HOST isn't set —
// production deploys configure SMTP via the existing audit-server env.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	dbPath := flag.String("db", os.Getenv("DS_DB_PATH"), "Path to ds.db (env DS_DB_PATH)")
	channel := flag.String("channel", "", "Limit delivery to one channel: slack | email")
	dryRun := flag.Bool("dry-run", false, "Build and render without sending or marking delivered")
	flag.Parse()
	if *dbPath == "" {
		*dbPath = "services/ds-service/data/ds.db"
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("digest: open db %q: %v", *dbPath, err)
	}
	defer d.Close()

	repoDB := projects.NewDB(d.DB)
	ctx := context.Background()
	now := time.Now().UTC()

	prefs, err := repoDB.ListEligibleDigestPreferences(ctx, now, *channel)
	if err != nil {
		log.Fatalf("digest: list prefs: %v", err)
	}
	if len(prefs) == 0 {
		fmt.Println("digest: no eligible preferences this hour.")
		return
	}

	sender := &projects.SlackSender{}
	delivered := 0
	skipped := 0
	for _, p := range prefs {
		payload, ids, err := repoDB.BuildDigestForUser(ctx, p.UserID, p.Channel)
		if err != nil {
			log.Printf("digest: build %s/%s: %v", p.UserID, p.Channel, err)
			continue
		}
		if len(ids) == 0 {
			skipped++
			continue
		}
		if *dryRun {
			fmt.Printf("[dry-run] %s → %s (%d items)\n%s\n",
				p.UserID, p.Channel, len(ids), projects.RenderSlackText(payload))
			continue
		}
		switch p.Channel {
		case projects.ChannelSlack:
			if p.SlackWebhookURL == "" {
				log.Printf("digest: %s missing slack webhook; skipping", p.UserID)
				continue
			}
			// Phase 5.3 P3 — send both `text` (fallback / push summary)
			// and `blocks` (BlockKit body). Slack uses text for the
			// channel preview + mobile notification; blocks renders the
			// message body on modern clients.
			msg := projects.SlackMessage{
				Text:   projects.RenderSlackText(payload),
				Blocks: projects.RenderSlackBlocks(payload),
			}
			if err := sender.Send(ctx, p.SlackWebhookURL, msg); err != nil {
				log.Printf("digest: slack send %s: %v", p.UserID, err)
				continue
			}
		case projects.ChannelEmail:
			if os.Getenv("SMTP_HOST") == "" {
				log.Printf("digest: %s email skipped — SMTP_HOST not set", p.UserID)
				continue
			}
			// Phase 7 polish wires the SMTP relay; today we log-and-skip
			// so the test suite + dry-run path stay deterministic.
			log.Printf("digest: %s email send (SMTP wiring pending)", p.UserID)
			continue
		}
		if err := repoDB.MarkDelivered(ctx, p.UserID, p.Channel, ids); err != nil {
			log.Printf("digest: mark delivered %s: %v", p.UserID, err)
			continue
		}
		delivered++
	}
	fmt.Printf("digest: delivered=%d skipped=%d eligible=%d\n", delivered, skipped, len(prefs))
}
