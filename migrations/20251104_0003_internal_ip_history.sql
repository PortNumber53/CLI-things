-- Internal IP History Table
-- Stores historical and current internal IP addresses for devices

CREATE TABLE IF NOT EXISTS public.internal_ip_history (
    id SERIAL PRIMARY KEY,
    hostname TEXT NOT NULL,
    interface_name TEXT NOT NULL,
    ip INET NOT NULL,
    is_ipv6 BOOLEAN NOT NULL DEFAULT FALSE,
    mac_address TEXT,
    first_use_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_use_at TIMESTAMP WITH TIME ZONE,

    CONSTRAINT unique_active_ip UNIQUE (hostname, interface_name, ip, last_use_at)
);

-- Index for efficient lookups
CREATE INDEX IF NOT EXISTS idx_internal_ip_history_hostname ON public.internal_ip_history(hostname);
CREATE INDEX IF NOT EXISTS idx_internal_ip_history_active ON public.internal_ip_history(last_use_at) WHERE last_use_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_internal_ip_history_interface ON public.internal_ip_history(hostname, interface_name);

-- View for current active IPs
CREATE OR REPLACE VIEW public.current_internal_ips AS
SELECT
    hostname,
    interface_name,
    ip::TEXT as ip,
    is_ipv6,
    mac_address,
    first_use_at
FROM public.internal_ip_history
WHERE last_use_at IS NULL
ORDER BY hostname, interface_name;

-- Grant permissions (adjust as needed)
-- GRANT SELECT, INSERT, UPDATE ON public.internal_ip_history TO your_app_user;
-- GRANT SELECT ON public.current_internal_ips TO your_app_user;
