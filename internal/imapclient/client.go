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
func (c *Client) EnsureFolder(name string) error {
	c.log.Debug("Ensuring folder exists: %s", name)

	// Try to select; if that works it exists.
	data, err := c.raw.Select(name, nil).Wait()
	if err == nil {
		_ = data
		// Deselect by selecting INBOX (harmless).
		_, _ = c.raw.Select("INBOX", nil).Wait()
		return nil
	}

	// Create the folder.
	if err := c.raw.Create(name, nil).Wait(); err != nil {
		return fmt.Errorf("create folder %q: %w", name, err)
	}
	c.log.Info("Created folder: %s", name)
	return nil
}

// SearchOlderThan returns UIDs of messages in folder older than cutoff.
func (c *Client) SearchOlderThan(folder string, cutoff time.Time) ([]imap.UID, error) {
	c.log.Debug("Selecting folder: %s", folder)
	if _, err := c.raw.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	criteria := &imap.SearchCriteria{
		Before: cutoff,
	}
	c.log.Debug("Searching for messages before %s", cutoff.Format("2006-01-02"))
	data, err := c.raw.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("UID SEARCH: %w", err)
	}

	return data.AllUIDs(), nil
}

// FetchInternalDate fetches the INTERNALDATE of a set of UIDs.
// Returns a map[UID]time.Time.
func (c *Client) FetchInternalDate(uids []imap.UID) (map[imap.UID]time.Time, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	set := imap.UIDSetNum(uids...)
	fetchOpts := &imap.FetchOptions{
		InternalDate: true,
	}

	msgs, err := c.raw.Fetch(set, fetchOpts).Collect()
	if err != nil {
		return nil, fmt.Errorf("FETCH INTERNALDATE: %w", err)
	}

	result := make(map[imap.UID]time.Time, len(msgs))
	for _, msg := range msgs {
		result[msg.UID] = msg.InternalDate
	}
	return result, nil
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
