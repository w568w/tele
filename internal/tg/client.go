package tg

import (
	"context"
	"net"
	"os"
	"sync"

	"github.com/gotd/log/logzap"
	"go.uber.org/zap"
	"golang.org/x/net/proxy"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/updates"
	updhook "github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/version"
)

// GotdClient wraps the gotd telegram client and implements the Client interface
type GotdClient struct {
	mu           sync.RWMutex
	api          *tg.Client
	mustDeliver  chan store.Event
	droppable    chan store.Event
	updates      chan store.Event
	peers        map[int64]domain.Peer
	log          *zap.Logger
	traceLog     *zap.Logger
	suppressMu   sync.Mutex
	suppressIDs  map[int]struct{}
	stateStorage updates.StateStorage

	customEmojiMu sync.RWMutex
	customEmoji   map[int64]string
	// senderNames remembers userID -> display name across updates and history
	// fetches so a live update that omits the sender's entity still resolves the
	// author instead of rendering "?" (#161).
	senderNames *nameCache
}

func NewGotdClient(log *zap.Logger, stateStorage updates.StateStorage, trace bool) *GotdClient {
	traceLog := zap.NewNop()
	if trace {
		traceLog = log
	}
	return &GotdClient{
		mustDeliver:  make(chan store.Event, 256),
		droppable:    make(chan store.Event, 64),
		updates:      make(chan store.Event, 32),
		peers:        make(map[int64]domain.Peer),
		log:          log,
		traceLog:     traceLog,
		suppressIDs:  make(map[int]struct{}),
		stateStorage: stateStorage,
		senderNames:  newNameCache(),

		customEmoji: make(map[int64]string),
	}
}

// acquireAPI returns the live API client under the read lock, or an error when
// the client is not connected yet. The failure never reaches the middleware —
// there is no RPC — so it is classified here.
func (c *GotdClient) acquireAPI() (*tg.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.api == nil {
		return nil, &telerr.Error{Kind: telerr.Network, Op: "acquire api", Detail: "not connected", Transient: true}
	}
	return c.api, nil
}

// Connect starts the gotd client. Call in a goroutine — blocks until ctx is cancelled.
// Closes readyCh once auth is complete and the updates loop has started.
// onAuth is called with the authenticated user ID and username before readyCh
// is closed; may be nil.
func (c *GotdClient) Connect(ctx context.Context, cfg *config.Config, af *AuthFlow, readyCh chan<- struct{}, onAuth func(int64, string)) error {
	sess := NewFileSession(cfg.Telegram.SessionFile)

	dispatcher := tg.NewUpdateDispatcher()
	setupDispatcher(&dispatcher, c.mustDeliver, c.droppable, c.log, func(id int) bool {
		c.suppressMu.Lock()
		defer c.suppressMu.Unlock()
		if _, ok := c.suppressIDs[id]; ok {
			delete(c.suppressIDs, id)
			return true
		}
		return false
	}, c.senderNames)

	// updates.New does not return an error — confirmed via go doc.
	updCfg := updates.Config{
		Handler: dispatcher,
		Storage: c.stateStorage,
		// Without an explicit logger updates.Manager defaults to zap.NewNop(),
		// discarding all of its gap/idle-timeout/getDifference diagnostics. Wire
		// our logger so the manager's recovery behavior after a long idle is
		// observable (#119): "Idle timeout", "Getting difference", "Pts gap
		// timeout" and channel-difference results all surface at debug level.
		// gotd v0.154.0 changed Logger from *zap.Logger to gotd/log.Logger;
		// wrap our zap logger with the logzap adapter to keep zap out of gotd's core graph.
		Logger: logzap.New(c.log.Named("updates")),
	}
	// Persist channel access hashes so channels are re-registered at startup and
	// UpdateChannelTooLong after a long idle is acted upon instead of dropped
	// (#119). Falls back to gotd's in-memory hasher if the storage does not
	// implement it.
	if h, ok := c.stateStorage.(updates.ChannelAccessHasher); ok {
		updCfg.AccessHasher = h
	}
	manager := updates.New(updCfg)

	// outboxHook intercepts UpdateReadHistoryOutbox / UpdateReadChannelOutbox before
	// the pts-tracking layer. updates.Manager silently drops these when a pts gap is
	// present (the pending buffer never flushes), so we extract them from the raw
	// wire message and emit the event immediately, then hand the update on to the
	// manager as usual.
	hook := newOutboxHook(manager, c.mustDeliver, c.log)

	// resync forces a full catch-up (getDifference) on every reconnect after the
	// first, so state missed during OS sleep/suspend is reconciled immediately on
	// wake instead of waiting for the manager's frozen 15-minute idle timer (#173).
	// UpdatesTooLong is the manager's own "you are too far behind, fetch the
	// difference" signal, so it drives the same common-state + channel catch-up
	// the server would ask for. Run off the connection-lifecycle callback (which
	// must not block) in a goroutine bounded by ctx: Handle can block on the
	// manager's internal queue.
	resync := &reconnectResync{
		force: func() {
			go func() {
				c.log.Info("reconnected, forcing updates catch-up")
				if err := manager.Handle(ctx, &tg.UpdatesTooLong{}); err != nil {
					c.log.Warn("forced updates catch-up after reconnect failed", zap.Error(err))
				}
			}()
		},
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt := <-c.mustDeliver:
				evt = c.hydrateEventCustomEmoji(ctx, evt)
				select {
				case c.updates <- evt:
				case <-ctx.Done():
					return
				}
			case evt := <-c.droppable:
				evt = c.hydrateEventCustomEmoji(ctx, evt)
				select {
				case c.updates <- evt:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	dialer := proxy.FromEnvironment()
	if dialer != proxy.Direct {
		c.log.Info("using system proxy from ALL_PROXY")
	}
	resolver := dcs.Plain(dcs.PlainOptions{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return dialer.Dial(network, addr)
		},
	})

	c.log.Info("gotd client", zap.String("gotd", gotdVersion()))

	tc := telegram.NewClient(cfg.Telegram.APIID, cfg.Telegram.APIHash, telegram.Options{
		UpdateHandler:  hook,
		SessionStorage: sess,
		Resolver:       resolver,
		// The "v" field carries the application version on every line, so gotd's
		// own stamp of the same name is dropped and reported once above instead.
		Logger: logzap.New(withoutField(c.log, "v")),
		// Names this app and this machine in Telegram's active-sessions list;
		// without it gotd reports the Go toolchain and its own version (#200).
		Device: deviceConfig(version.Version, os.Hostname),
		// OnDead marks MTProto connection death (and the reconnect that follows)
		// so a long-idle update stall (#119) can be correlated with connection
		// drops in the logs. Logged at warn so it shows without -e.
		OnDead: func(err error) {
			c.log.Warn("mtproto connection dead", zap.Error(err))
		},
		// OnConnectionState drives the post-reconnect catch-up (#173): gotd emits
		// ConnectionStateReady on every (re)established primary connection, which
		// is the reliable wake/reconnect signal the frozen idle timer is not.
		OnConnectionState: resync.onConnectionState,
		// Maps every RPC error onto the domain taxonomy exactly once, so tgerr
		// never leaves this package (#191).
		// errorMiddleware maps every RPC error onto the domain taxonomy; the
		// update hook feeds the updates an RPC *reply* carries into the same
		// pipeline the pushed ones go through.
		//
		// Telegram does not push an echo for what this client itself did: the
		// created message comes back in the reply to messages.forwardMessages (or
		// sendMessage, sendMedia) and nowhere else, until an unrelated pts gap
		// forces a getDifference. Without this hook a forward stayed invisible
		// until the other side happened to read it (#198).
		Middlewares: []telegram.Middleware{c.errorMiddleware(), updhook.UpdateHook(hook.Handle)},
	})

	c.log.Debug("connecting to telegram")
	return tc.Run(ctx, func(ctx context.Context) error {
		c.log.Debug("running auth flow")
		flow := auth.NewFlow(af, auth.SendCodeOptions{})
		if err := tc.Auth().IfNecessary(ctx, flow); err != nil {
			c.log.Error("auth failed", zap.Error(err))
			return err
		}

		self, err := tc.Self(ctx)
		if err != nil {
			c.log.Error("Self() failed", zap.Error(err))
			return err
		}
		c.log.Info("authenticated", zap.Int64("user_id", self.ID))

		if onAuth != nil {
			onAuth(self.ID, self.Username)
		}

		c.mu.Lock()
		c.api = tc.API()
		c.mu.Unlock()

		return manager.Run(ctx, tc.API(), self.ID, updates.AuthOptions{
			OnStart: func(ctx context.Context) {
				c.log.Debug("updates manager started, signalling ready")
				close(readyCh)
			},
		})
	})
}

func (c *GotdClient) Updates() <-chan store.Event {
	return c.updates
}
