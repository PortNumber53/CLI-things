-- publicip tables
CREATE TABLE IF NOT EXISTS public.public_ip_history (
    ip inet PRIMARY KEY,
    first_use_at timestamptz NOT NULL DEFAULT now(),
    last_use_at timestamptz
);

CREATE TABLE IF NOT EXISTS public.dns_targets (
    fqdn text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS public.dns_history (
    fqdn text NOT NULL,
    ip inet NOT NULL,
    first_use_at timestamptz NOT NULL DEFAULT now(),
    last_use_at timestamptz,
    PRIMARY KEY (fqdn, ip)
);

