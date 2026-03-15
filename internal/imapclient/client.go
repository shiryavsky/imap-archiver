package imapclient

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"imap-archiver/internal/config"
	"imap-archiver/internal/logger"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Client wraps the go-imap client with convenience helpers.
type Client struct {
	raw *imapclient.Client
	cfg *config.Config
	log *logger.Logger
}

// Connect establishes an authenticated IMAP session.
func Connect(cfg *config.Config, log *logger.Logger) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Info("Connecting to %s (TLS=%v STARTTLS=%v)…", addr, cfg.TLS, cfg.StartTLS)

	var raw *imapclient.Client
	var err error

	tlsCfg := &tls.Config{ServerName: cfg.Host}

	switch {
	case cfg.StartTLS:
		conn, dialErr := net.DialTimeout("tcp", addr, 30*time.Second)
		if dialErr != nil {
			return nil, fmt.Errorf("TCP dial: %w", dialErr)
		}
		raw, err = imapclient.NewStartTLS(conn, &imapclient.Options{})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("IMAP STARTTLS: %w", err)
		}
	case cfg.TLS:
		raw, err = imapclient.DialTLS(addr, &imapclient.Options{
			TLSConfig: tlsCfg,
		})
		if err != nil {
			return nil, fmt.Errorf("IMAPS dial: %w", err)
		}

	default:
		conn, dialErr := net.DialTimeout("tcp", addr, 30*time.Second)
		if dialErr != nil {
			return nil, fmt.Errorf("TCP dial: %w", dialErr)
		}
		raw = imapclient.New(conn, &imapclient.Options{})
		// if err != nil {
		// 	conn.Close()
		// 	return nil, fmt.Errorf("IMAP handshake: %w", err)
		// }
	}

	log.Debug("Connected; logging in as %s", cfg.Username)
	if err := raw.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	log.Info("Logged in as %s", cfg.Username)

	return &Client{raw: raw, cfg: cfg, log: log}, nil
}

// Close logs out and disconnects.
func (c *Client) Close() {
	_ = c.raw.Logout().Wait()
}

// EnsureFolder creates a mailbox if it does not already exist.
// Note: This function may leave the connection in an inconsistent state (selected folder changed).
// Callers should re-select the source folder after this function returns.
func (c *Client) EnsureFolder(name string) error {
	c.log.Debug("Ensuring folder exists: %s", name)

	// Try to select; if that works it exists.
	_, err := c.raw.Select(name, nil).Wait()
	if err == nil {
		c.log.Debug("Folder %q already exists", name)
		return nil
	}

	// Create the folder.
	if err := c.raw.Create(name, nil).Wait(); err != nil {
		return fmt.Errorf("create folder %q: %w", name, err)
	}
	c.log.Info("Created folder: %s", name)
	return nil
}

// YearBucket is a set of UIDs that all belong to a single calendar year.
type YearBucket struct {
	Year int
	UIDs []imap.UID
}

// SelectFolder selects an IMAP folder. Must be called before searches.
func (c *Client) SelectFolder(folder string) error {
	c.log.Debug("Selecting folder: %s", folder)
	if _, err := c.raw.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	return nil
}

// SearchByYearRange queries the server once per calendar year using
// SEARCH SINCE <Jan 1 YYYY> BEFORE <Jan 1 YYYY+1>, avoiding any FETCH call.
// It sweeps from (cutoff.Year - 1) down to minYear, stopping as soon as two
// consecutive empty years are seen (heuristic: mailbox floor reached).
//
// The folder must already be selected via SelectFolder.
func (c *Client) SearchByYearRange(cutoff time.Time, minYear int) ([]YearBucket, error) {
	endYear := cutoff.Year() - 1 // messages from cutoff.Year are not yet archivable
	if endYear < minYear {
		return nil, nil
	}

	c.log.Debug("Sweeping years %d → %d using server-side SEARCH", endYear, minYear)

	var buckets []YearBucket
	emptyStreak := 0

	for year := endYear; year >= minYear; year-- {
		jan1 := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		jan1Next := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)

		// Skip the partial current year — only use the BEFORE cutoff bound for it.
		// For the cutoff year itself the caller already handled the boundary.
		criteria := &imap.SearchCriteria{
			Since:  jan1,
			Before: jan1Next,
		}
		// For the most recent archivable year, cap at cutoff.
		if year == endYear {
			criteria.Before = cutoff
		}

		data, err := c.raw.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("SEARCH year %d: %w", year, err)
		}

		uids := data.AllUIDs()
		c.log.Debug("  Year %d: %d message(s)", year, len(uids))

		if len(uids) == 0 {
			emptyStreak++
			if emptyStreak >= 3 {
				c.log.Debug("  Three consecutive empty years — stopping sweep at %d", year)
				break
			}
			continue
		}
		emptyStreak = 0
		buckets = append(buckets, YearBucket{Year: year, UIDs: uids})
	}

	return buckets, nil
}

// FetchInternalDatePaged fetches INTERNALDATE for uids in pages of pageSize,
// to avoid overwhelming servers with a single giant FETCH command.
// This is a fallback for when SearchByYearRange cannot be used.
func (c *Client) FetchInternalDatePaged(uids []imap.UID, pageSize int) (map[imap.UID]time.Time, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	if pageSize <= 0 {
		pageSize = 500
	}

	result := make(map[imap.UID]time.Time, len(uids))
	pages := pageSlice(uids, pageSize)

	for i, page := range pages {
		c.log.Debug("  FETCH INTERNALDATE page %d/%d (%d UIDs)…", i+1, len(pages), len(page))
		set := imap.UIDSetNum(page...)
		msgs, err := c.raw.Fetch(set, &imap.FetchOptions{InternalDate: true}).Collect()
		if err != nil {
			return nil, fmt.Errorf("FETCH page %d: %w", i+1, err)
		}
		for _, msg := range msgs {
			result[msg.UID] = msg.InternalDate
		}
	}
	return result, nil
}

// pageSlice splits a UID slice into pages of at most n items.
func pageSlice(uids []imap.UID, n int) [][]imap.UID {
	var pages [][]imap.UID
	for len(uids) > 0 {
		sz := n
		if sz > len(uids) {
			sz = len(uids)
		}
		pages = append(pages, uids[:sz])
		uids = uids[sz:]
	}
	return pages
}

// MoveUIDs moves a set of UIDs to destFolder using IMAP MOVE (or COPY+EXPUNGE).
func (c *Client) MoveUIDs(uids []imap.UID, destFolder string) error {
	if len(uids) == 0 {
		return nil
	}
	set := imap.UIDSetNum(uids...)

	// Use UID MOVE if available (RFC 6851).
	if _, err := c.raw.Move(set, destFolder).Wait(); err != nil {
		// Fallback: COPY + STORE \Deleted + EXPUNGE
		c.log.Debug("MOVE unsupported, falling back to COPY+DELETE: %v", err)
		if _, err2 := c.raw.Copy(set, destFolder).Wait(); err2 != nil {
			return fmt.Errorf("COPY to %q: %w", destFolder, err2)
		}
		storeFlags := &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagDeleted},
			Silent: true,
		}
		if err2 := c.raw.Store(set, storeFlags, nil).Close(); err2 != nil {
			return fmt.Errorf("STORE \\Deleted: %w", err2)
		}
		if err2 := c.raw.Expunge().Close(); err2 != nil {
			return fmt.Errorf("EXPUNGE: %w", err2)
		}
	}
	return nil
}

// Capabilities returns server capability strings.
func (c *Client) Capabilities() []string {
	caps := c.raw.Caps()
	var out []string
	for cap := range caps {
		out = append(out, string(cap))
	}
	return out
}
