// Package authredis stores Fabrin's ephemeral authentication state in a
// standalone Redis primary. It owns its Redis client and exposes no client type.
package authredis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/usefabrin/fabrin/auth"
)

const defaultPrefix = "fabrin:auth:v1:"

// Option configures Store construction.
type Option func(*settings)

// WithPrefix isolates authentication keys under a non-secret Redis namespace.
// A trailing colon is added when absent.
func WithPrefix(prefix string) Option { return func(s *settings) { s.prefix = prefix } }

type settings struct{ prefix string }

// Store implements auth.Store and auth.SessionStore with Redis scripts and a
// durable identity resolver.
type Store struct {
	client     *redis.Client
	identities auth.IdentityStore
	prefix     string
}

// New parses redisURL and constructs a store without connecting. Supported URLs
// use redis:// or rediss://; call Ping from readiness to test connectivity.
func New(redisURL string, identities auth.IdentityStore, options ...Option) (*Store, error) {
	if identities == nil {
		return nil, errors.New("authredis: identity store is required")
	}
	parsed, err := url.Parse(redisURL)
	if err != nil || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") || parsed.Host == "" {
		return nil, errors.New("authredis: valid redis:// or rediss:// URL is required")
	}
	config := settings{prefix: defaultPrefix}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("authredis: option %d is nil", i)
		}
		option(&config)
	}
	if !validPrefix(config.prefix) {
		return nil, errors.New("authredis: prefix must contain 1-128 safe ASCII characters")
	}
	if !strings.HasSuffix(config.prefix, ":") {
		config.prefix += ":"
	}
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("authredis: parse URL: %w", err)
	}
	return &Store{client: redis.NewClient(redisOptions), identities: identities, prefix: config.prefix}, nil
}

// Ping checks Redis connectivity for an application readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("authredis: ping: %w", err)
	}
	return nil
}

// Close releases the owned Redis client and its connection pool.
func (s *Store) Close() error { return s.client.Close() }

// Reserve atomically consumes send budgets and replaces the active challenge.
func (s *Store) Reserve(ctx context.Context, reservation auth.Reservation) error {
	address := digest(reservation.Email)
	source := digest(reservation.Source)
	result, err := reserveScript.Run(ctx, s.client, []string{
		s.prefix + "send:address:" + address,
		s.prefix + "send:source:" + source,
		s.prefix + "active:" + digest(string(reservation.Purpose)+"\x00"+reservation.Email),
		s.prefix + "challenge:" + reservation.ID,
	}, s.prefix+"challenge:", reservation.ID, reservation.Email, address, reservation.KeyID, string(reservation.Purpose), hex.EncodeToString(reservation.Verifier[:])).Int64()
	if err != nil {
		return err
	}
	if result == 2 {
		return auth.ErrRateLimited
	}
	if result != 1 {
		return errors.New("authredis: invalid reserve result")
	}
	return nil
}

// Verify leases one valid challenge, resolves its durable identity, then
// atomically consumes the challenge and creates the digest-only session.
func (s *Store) Verify(ctx context.Context, attempt auth.Verification) (auth.Identity, error) {
	lease := digest(attempt.Session.ID + "\x00" + hex.EncodeToString(attempt.Session.Digest[:]))
	result, err := verifyScript.Run(ctx, s.client, []string{
		s.prefix + "verify:source:" + digest(attempt.Source),
		s.prefix + "challenge:" + attempt.ID,
	}, s.prefix+"verify:address:", lease, attempt.Email, attempt.KeyID, string(attempt.Purpose), hex.EncodeToString(attempt.Verifier[:])).Int64()
	if err != nil {
		return auth.Identity{}, err
	}
	switch result {
	case 2:
		return auth.Identity{}, auth.ErrRateLimited
	case 0:
		return auth.Identity{}, auth.ErrAuthentication
	case 1:
	default:
		return auth.Identity{}, errors.New("authredis: invalid verify result")
	}

	identity, err := s.identities.ResolveVerified(ctx, auth.IdentityResolution{Email: attempt.Email, ProposedID: attempt.IdentityID, VerifiedAt: attempt.Now})
	if err != nil {
		if errors.Is(err, auth.ErrAuthentication) {
			cleanupCtx, cancel := cleanupContext(ctx)
			defer cancel()
			_, consumeErr := rejectLeaseScript.Run(cleanupCtx, s.client, []string{s.prefix + "challenge:" + attempt.ID}, lease).Result()
			if consumeErr != nil {
				return auth.Identity{}, consumeErr
			}
			return auth.Identity{}, auth.ErrAuthentication
		}
		cleanupCtx, cancel := cleanupContext(ctx)
		defer cancel()
		_, _ = releaseLeaseScript.Run(cleanupCtx, s.client, []string{s.prefix + "challenge:" + attempt.ID}, lease).Result()
		return auth.Identity{}, err
	}
	if !validSession(attempt.Session) {
		cleanupCtx, cancel := cleanupContext(ctx)
		defer cancel()
		_, _ = releaseLeaseScript.Run(cleanupCtx, s.client, []string{s.prefix + "challenge:" + attempt.ID}, lease).Result()
		return auth.Identity{}, errors.New("authredis: invalid session record")
	}
	duration := attempt.Session.ExpiresAt.Sub(attempt.Session.CreatedAt)
	complete, err := completeScript.Run(ctx, s.client, []string{
		s.prefix + "challenge:" + attempt.ID,
		s.prefix + "complete:" + lease,
		s.prefix + "session:" + attempt.Session.ID,
		s.identitySessionsKey(identity.ID),
	}, lease, attempt.Session.ID, hex.EncodeToString(attempt.Session.Digest[:]), identity.ID, identity.Email, identity.CreatedAt.UnixNano(), duration.Milliseconds()).Int64()
	if err != nil {
		// Retrying the idempotent completion distinguishes an ambiguous response
		// from a command that never reached Redis.
		finalizeCtx, cancel := cleanupContext(ctx)
		defer cancel()
		complete, err = completeScript.Run(finalizeCtx, s.client, []string{
			s.prefix + "challenge:" + attempt.ID,
			s.prefix + "complete:" + lease,
			s.prefix + "session:" + attempt.Session.ID,
			s.identitySessionsKey(identity.ID),
		}, lease, attempt.Session.ID, hex.EncodeToString(attempt.Session.Digest[:]), identity.ID, identity.Email, identity.CreatedAt.UnixNano(), duration.Milliseconds()).Int64()
	}
	if err != nil {
		return auth.Identity{}, err
	}
	if complete != 1 {
		return auth.Identity{}, auth.ErrAuthentication
	}
	return identity, nil
}

// Invalidate disables exactly the named challenge.
func (s *Store) Invalidate(ctx context.Context, id string) error {
	return invalidateScript.Run(ctx, s.client, []string{s.prefix + "challenge:" + id}).Err()
}

// AuthenticateSession verifies a digest, enforces Redis-server idle/absolute
// expiry, and refreshes last activity atomically.
func (s *Store) AuthenticateSession(ctx context.Context, proof auth.SessionProof) (auth.Identity, error) {
	values, err := sessionScript.Run(ctx, s.client, []string{s.prefix + "session:" + proof.ID}, hex.EncodeToString(proof.Digest[:])).StringSlice()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return auth.Identity{}, auth.ErrSession
		}
		return auth.Identity{}, err
	}
	if len(values) != 3 || values[0] == "" {
		return auth.Identity{}, auth.ErrSession
	}
	created, err := strconv.ParseInt(values[2], 10, 64)
	if err != nil {
		return auth.Identity{}, errors.New("authredis: corrupt session identity time")
	}
	return auth.Identity{ID: values[0], Email: values[1], CreatedAt: time.Unix(0, created).UTC()}, nil
}

// RevokeSession removes a session only when its secret digest matches.
func (s *Store) RevokeSession(ctx context.Context, proof auth.SessionProof) error {
	result, err := revokeScript.Run(ctx, s.client, []string{s.prefix + "session:" + proof.ID}, hex.EncodeToString(proof.Digest[:]), s.prefix+"session:").Int64()
	if err != nil {
		return err
	}
	if result != 1 {
		return auth.ErrSession
	}
	return nil
}

// RevokeAllSessions authenticates one session and removes every session indexed
// for the same identity in one Redis transition.
func (s *Store) RevokeAllSessions(ctx context.Context, proof auth.SessionProof) error {
	result, err := revokeAllScript.Run(ctx, s.client, []string{s.prefix + "session:" + proof.ID}, hex.EncodeToString(proof.Digest[:]), s.prefix+"session:").Int64()
	if err != nil {
		return err
	}
	if result != 1 {
		return auth.ErrSession
	}
	return nil
}

func (s *Store) identitySessionsKey(identityID string) string {
	return s.prefix + "identity-sessions:" + digest(identityID)
}

func validPrefix(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != ':' && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validSession(record auth.SessionRecord) bool {
	return record.ID != "" && !record.CreatedAt.IsZero() && record.LastSeenAt.Equal(record.CreatedAt) && record.ExpiresAt.After(record.CreatedAt)
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
}

var reserveScript = redis.NewScript(`
local now = redis.call('TIME'); local ms = now[1]*1000 + math.floor(now[2]/1000)
local window = 3600000
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ms-window)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ms-window)
if redis.call('ZCARD', KEYS[1]) >= 5 or redis.call('ZCARD', KEYS[2]) >= 20 then return 2 end
local last = redis.call('ZRANGE', KEYS[1], -1, -1, 'WITHSCORES')
if #last == 2 and ms-tonumber(last[2]) < 60000 then return 2 end
local member = tostring(ms)..':'..ARGV[2]
redis.call('ZADD', KEYS[1], ms, member); redis.call('ZADD', KEYS[2], ms, member)
redis.call('PEXPIRE', KEYS[1], window+60000); redis.call('PEXPIRE', KEYS[2], window+60000)
local previous = redis.call('GET', KEYS[3])
if previous and redis.call('EXISTS', ARGV[1]..previous) == 1 then redis.call('HSET', ARGV[1]..previous, 'active', '0') end
redis.call('SET', KEYS[3], ARGV[2], 'PX', 300000)
redis.call('HSET', KEYS[4], 'email', ARGV[3], 'address', ARGV[4], 'key_id', ARGV[5], 'purpose', ARGV[6], 'verifier', ARGV[7], 'attempts', '0', 'active', '1')
redis.call('PEXPIRE', KEYS[4], 300000)
return 1`)

var verifyScript = redis.NewScript(`
local function equal(a,b)
  if not a or not b or string.len(a) ~= string.len(b) then return false end
  local different = 0
  for i=1,string.len(a) do if string.byte(a,i) ~= string.byte(b,i) then different = 1 end end
  return different == 0
end
local now = redis.call('TIME'); local ms = now[1]*1000 + math.floor(now[2]/1000); local window=3600000
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ms-window)
if redis.call('ZCARD', KEYS[1]) >= 100 then return 2 end
redis.call('ZADD', KEYS[1], ms, tostring(ms)..':'..ARGV[2]); redis.call('PEXPIRE', KEYS[1], window+60000)
if redis.call('EXISTS', KEYS[2]) == 0 then return 0 end
local values=redis.call('HMGET',KEYS[2],'email','address','key_id','purpose','verifier','attempts','active','lease','lease_until')
if values[7] ~= '1' then return 0 end
if values[8] and tonumber(values[9]) > ms then return 0 end
if values[8] then redis.call('HDEL',KEYS[2],'lease','lease_until') end
if not (equal(values[1],ARGV[3]) and equal(values[3],ARGV[4]) and equal(values[4],ARGV[5]) and equal(values[5],ARGV[6])) then
  local failures=ARGV[1]..values[2]; redis.call('ZREMRANGEBYSCORE',failures,'-inf',ms-window)
  if redis.call('ZCARD',failures) >= 20 then return 2 end
  redis.call('ZADD',failures,ms,tostring(ms)..':'..ARGV[2]); redis.call('PEXPIRE',failures,window+60000)
  local attempts=redis.call('HINCRBY',KEYS[2],'attempts',1); if attempts >= 5 then redis.call('HSET',KEYS[2],'active','0') end
  return 0
end
redis.call('HSET',KEYS[2],'lease',ARGV[2],'lease_until',ms+30000)
return 1`)

var releaseLeaseScript = redis.NewScript(`if redis.call('HGET',KEYS[1],'lease') == ARGV[1] then redis.call('HDEL',KEYS[1],'lease','lease_until'); return 1 end return 0`)
var rejectLeaseScript = redis.NewScript(`if redis.call('HGET',KEYS[1],'lease') == ARGV[1] then redis.call('HSET',KEYS[1],'active','0'); redis.call('HDEL',KEYS[1],'lease','lease_until'); return 1 end return 0`)
var invalidateScript = redis.NewScript(`if redis.call('EXISTS',KEYS[1]) == 1 then redis.call('HSET',KEYS[1],'active','0') end return 1`)

var completeScript = redis.NewScript(`
local already=redis.call('GET',KEYS[2])
if already == ARGV[1] then return 1 end
if redis.call('HGET',KEYS[1],'lease') ~= ARGV[1] then return 0 end
local now=redis.call('TIME'); local ms=now[1]*1000 + math.floor(now[2]/1000); local duration=tonumber(ARGV[7])
redis.call('HSET',KEYS[3],'digest',ARGV[3],'identity_id',ARGV[4],'email',ARGV[5],'identity_created',ARGV[6],'created',ms,'last_seen',ms,'expires',ms+duration,'identity_sessions',KEYS[4])
redis.call('PEXPIRE',KEYS[3],duration); redis.call('SADD',KEYS[4],ARGV[2]); redis.call('PEXPIRE',KEYS[4],duration)
redis.call('SET',KEYS[2],ARGV[1],'PX',300000); redis.call('DEL',KEYS[1]); return 1`)

var sessionScript = redis.NewScript(`
local function equal(a,b)
  if not a or not b or string.len(a) ~= string.len(b) then return false end
  local different=0; for i=1,string.len(a) do if string.byte(a,i) ~= string.byte(b,i) then different=1 end end
  return different == 0
end
local v=redis.call('HMGET',KEYS[1],'digest','identity_id','email','identity_created','created','last_seen','expires','identity_sessions')
if not v[1] or not equal(v[1],ARGV[1]) then return {} end
local now=redis.call('TIME'); local ms=now[1]*1000 + math.floor(now[2]/1000)
if ms >= tonumber(v[7]) or ms >= tonumber(v[6])+86400000 then redis.call('DEL',KEYS[1]); return {} end
redis.call('HSET',KEYS[1],'last_seen',ms); redis.call('PEXPIREAT',KEYS[1],tonumber(v[7])); return {v[2],v[3],v[4]}`)

var revokeScript = redis.NewScript(`
local values=redis.call('HMGET',KEYS[1],'digest','identity_sessions'); local value=values[1]; if not value or string.len(value) ~= string.len(ARGV[1]) then return 0 end
local different=0; for i=1,string.len(value) do if string.byte(value,i) ~= string.byte(ARGV[1],i) then different=1 end end
if different ~= 0 then return 0 end
if values[2] then redis.call('SREM',values[2],string.sub(KEYS[1],string.len(ARGV[2])+1)) end
redis.call('DEL',KEYS[1]); return 1`)

var revokeAllScript = redis.NewScript(`
local function equal(a,b)
  if not a or not b or string.len(a) ~= string.len(b) then return false end
  local different=0; for i=1,string.len(a) do if string.byte(a,i) ~= string.byte(b,i) then different=1 end end
  return different == 0
end
local v=redis.call('HMGET',KEYS[1],'digest','identity_sessions','created','last_seen','expires')
if not v[1] or not v[2] or not equal(v[1],ARGV[1]) then return 0 end
local now=redis.call('TIME'); local ms=now[1]*1000 + math.floor(now[2]/1000)
if ms >= tonumber(v[5]) or ms >= tonumber(v[4])+86400000 then redis.call('DEL',KEYS[1]); return 0 end
local sessions=redis.call('SMEMBERS',v[2])
for _,id in ipairs(sessions) do redis.call('DEL',ARGV[2]..id) end
redis.call('DEL',v[2]); return 1`)

var (
	_ auth.Store        = (*Store)(nil)
	_ auth.SessionStore = (*Store)(nil)
)
