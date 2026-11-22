package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A watch is a search to repeat until it finds something: "tell me when X is
// out". A timer runs `watch run`; when a watched search has a seeded result
// the owner gets a message (with the .torrent file, if the watch says so) and
// the watch is done.

type watch struct {
	Args  []string  `json:"args"` // the words and options, as given
	Added time.Time `json:"added"`
}

func watchPath() string { return filepath.Join(dataDir(), "watch.json") }

func loadWatches() []watch {
	var ws []watch
	loadJSON(watchPath(), &ws)
	return ws
}

func (t *tool) watch(ctx context.Context, args []string) error {
	const usage = "usage: watch add WORDS [--quality 1080p] [--get] | watch list | watch remove N | watch run"
	if len(args) == 0 {
		return errors.New(usage)
	}
	ws := loadWatches()
	switch args[0] {
	case "add":
		o, err := parseOptions(args[1:])
		if err != nil || len(o.words) == 0 {
			return errors.Join(err, errors.New(usage))
		}
		if err := t.needSite(); err != nil {
			return err
		}
		for _, w := range ws {
			if strings.EqualFold(strings.Join(w.Args, " "), strings.Join(args[1:], " ")) {
				fmt.Fprintf(t.out, "Already watching for %s.\n", describe(o))
				return nil
			}
		}
		ws = append(ws, watch{Args: args[1:], Added: time.Now()})
		if err := saveJSON(watchPath(), ws); err != nil {
			return err
		}
		also := ""
		if o.get {
			also = " and send him the .torrent file"
		}
		fmt.Fprintf(t.out, "WATCHING for %s on %s. It is checked a few times a day; when it appears with a seeder, Chinmay gets a message%s. Tell him that. To stop: novee-torrent watch remove %d\n",
			describe(o), t.cfg.Site, also, len(ws))
	case "list":
		if len(ws) == 0 {
			fmt.Fprintln(t.out, "Nothing is being watched for.")
			return nil
		}
		fmt.Fprintf(t.out, "Watching for %d:\n", len(ws))
		for i, w := range ws {
			fmt.Fprintf(t.out, "%d. %s (since %s)\n", i+1, strings.Join(w.Args, " "), w.Added.Format("01/02/2006"))
		}
	case "remove":
		if len(args) != 2 {
			return errors.New(usage)
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 || n > len(ws) {
			return fmt.Errorf("there is no watch number %s; see: novee-torrent watch list", args[1])
		}
		gone := ws[n-1]
		ws = append(ws[:n-1], ws[n:]...)
		if err := saveJSON(watchPath(), ws); err != nil {
			return err
		}
		fmt.Fprintf(t.out, "No longer watching for %s.\n", strings.Join(gone.Args, " "))
	case "run":
		var keep []watch
		for _, w := range ws {
			o, err := parseOptions(w.Args)
			if err != nil {
				continue
			}
			p, hits, total, err := t.results(ctx, o)
			if err != nil { // not signed in, or the site is down: keep everything and try next time
				return err
			}
			if len(hits) == 0 || hits[0].Seeds == 0 {
				fmt.Fprintf(t.out, "not yet: %s\n", describe(o))
				keep = append(keep, w)
				continue
			}
			lines := []string{fmt.Sprintf("Available now on %s: %s (%d results). The best:", t.cfg.Site, describe(o), total)}
			for i, h := range hits[:min(3, len(hits))] {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, h.line()))
			}
			fmt.Fprintln(t.out, strings.Join(lines, "\n"))
			if err := notifyOwner(strings.Join(lines, "\n")); err != nil {
				keep = append(keep, w) // could not tell him: stay on it
				continue
			}
			if o.get {
				_ = t.deliver(ctx, p, hits[:1], strings.Join(o.words, " "), true)
			}
		}
		return saveJSON(watchPath(), keep)
	default:
		return errors.New(usage)
	}
	return nil
}
