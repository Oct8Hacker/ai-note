-- Token bucket, one atomic step: refill by elapsed time, then take n tokens or
-- report how long to wait.
--
-- KEYS[1]  bucket hash key
-- ARGV[1]  capacity (tokens)
-- ARGV[2]  refill rate (tokens per second)
-- ARGV[3]  n, tokens requested
--
-- Time comes from the Redis server (TIME), not from the caller, so every
-- worker shares one clock. Returns 0 when the tokens were taken, otherwise
-- the wait in microseconds (nothing is taken in that case).
local cap = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local n = tonumber(ARGV[3])
if n > cap then n = cap end -- a request larger than the bucket can never succeed

local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local d = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(d[1])
local ts = tonumber(d[2])
if tokens == nil or ts == nil then
  tokens = cap
  ts = now
end

local elapsed = now - ts
if elapsed < 0 then elapsed = 0 end
tokens = math.min(cap, tokens + elapsed / 1000000 * refill)

local wait = 0
if tokens >= n - 1e-9 then
  tokens = tokens - n
else
  if refill <= 0 then
    wait = 1e13
  else
    wait = math.ceil((n - tokens) / refill * 1000000)
  end
end

redis.call('HSET', KEYS[1], 'tokens', string.format('%.9f', tokens), 'ts', string.format('%d', now))
-- Idle buckets (e.g. per-token Notion buckets) disappear once full again.
local ttl = 60000
if refill > 0 then ttl = math.max(ttl, math.ceil(cap / refill * 2000)) end
redis.call('PEXPIRE', KEYS[1], ttl)
return wait
