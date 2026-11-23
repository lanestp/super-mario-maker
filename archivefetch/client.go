package archivefetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const agent = "smm-private-server/1.0 (fetches a course when a player asks for it; one request at a time)"

// * The archive is two volunteer-run services, so every request to them
// * goes through one gate: one at a time, with a gap before each, and a
// * longer one each time a service says to slow down. A player waiting on
// * a course goes ahead of the background fill, with a shorter gap
type gate struct {
	mutex     sync.Mutex
	last      time.Time
	backoff   time.Duration
	waiting   int
	fillGap   time.Duration
	urgentGap time.Duration
}

func (g *gate) take(ctx context.Context, urgent bool) error {
	if urgent {
		g.mutex.Lock()
		g.waiting++
		g.mutex.Unlock()

		defer func() {
			g.mutex.Lock()
			g.waiting--
			g.mutex.Unlock()
		}()
	}

	for {
		g.mutex.Lock()

		gap := g.fillGap
		if urgent {
			gap = g.urgentGap
		}

		now := time.Now()
		ready := g.last.Add(gap + g.backoff)

		if (urgent || g.waiting == 0) && !now.Before(ready) {
			g.last = now
			g.mutex.Unlock()

			return nil
		}

		sleep := min(max(ready.Sub(now), 10*time.Millisecond), 250*time.Millisecond)
		g.mutex.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
}

func (g *gate) slowDown() {
	g.mutex.Lock()
	g.backoff = min(max(2*g.backoff, 30*time.Second), 30*time.Minute)
	g.mutex.Unlock()
}

func (g *gate) easeBack() {
	g.mutex.Lock()
	g.backoff /= 2
	g.mutex.Unlock()
}

type client struct {
	http    *http.Client
	index   string
	wayback string
	gate    *gate
}

// * Answers nil, with no error, when the service has nothing there
func (c *client) get(ctx context.Context, address string, urgent bool) ([]byte, error) {
	if err := c.gate.take(ctx, urgent); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", agent)

	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			c.gate.slowDown()
		}

		return nil, fmt.Errorf("%s could not be reached: %w", request.URL.Host, err)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	response.Body.Close()

	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode >= 520 {
		c.gate.slowDown()
		return nil, fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
	}

	if err != nil {
		return nil, fmt.Errorf("reading from %s failed: %w", request.URL.Host, err)
	}

	c.gate.easeBack()

	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
	}

	return body, nil
}

// * The index's entry for a course, or nil when it has none
func (c *client) byID(ctx context.Context, dataID uint64, urgent bool) (*Entry, error) {
	body, err := c.get(ctx, c.index+"level/"+strconv.FormatUint(dataID, 10), urgent)
	if err != nil || body == nil {
		return nil, err
	}

	var entry Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("the index's entry could not be read: %w", err)
	}

	if entry.LevelID != dataID {
		return nil, nil
	}

	return &entry, nil
}

// * The courses at or after a position in the index. A position past the
// * last course answers with an error object, which is no courses
func (c *client) atPosition(ctx context.Context, position int64) ([]Entry, error) {
	body, err := c.get(ctx, c.index+"searchRandomLevels/"+strconv.FormatInt(position, 10), false)
	if err != nil || body == nil {
		return nil, err
	}

	var entries []Entry
	if json.Unmarshal(body, &entries) != nil {
		return nil, nil
	}

	return entries, nil
}

// * The course file, or nil when the Wayback Machine has no copy of it
func (c *client) file(ctx context.Context, original string, urgent bool) ([]byte, error) {
	file, err := c.get(ctx, c.wayback+original, urgent)
	if err != nil {
		return nil, err
	}

	if !bytes.HasPrefix(file, []byte("ASH0")) {
		return nil, nil
	}

	return file, nil
}
