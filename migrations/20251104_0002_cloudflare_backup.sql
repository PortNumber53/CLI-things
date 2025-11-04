-- cloudflare-backup tables
CREATE TABLE IF NOT EXISTS public.cloudflare_accounts (
    id text PRIMARY KEY,
    name text NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    raw jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS public.cloudflare_zones (
    id text PRIMARY KEY,
    account_id text,
    name text NOT NULL,
    status text,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    raw jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS public.cloudflare_dns_records (
    zone_id text NOT NULL,
    id text NOT NULL,
    name text NOT NULL,
    type text NOT NULL,
    content text,
    ttl integer,
    proxied boolean,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    raw jsonb NOT NULL,
    PRIMARY KEY (zone_id, id)
);

CREATE TABLE IF NOT EXISTS public.cloudflare_backup_runs (
    id bigserial PRIMARY KEY,
    run_at timestamptz NOT NULL DEFAULT now(),
    accounts_collected integer NOT NULL DEFAULT 0,
    zones_collected integer NOT NULL DEFAULT 0,
    records_collected integer NOT NULL DEFAULT 0,
    success boolean NOT NULL DEFAULT true,
    error text
);

