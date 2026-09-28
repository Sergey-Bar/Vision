-- 0001_tenancy_and_topology.expand.sql
--
-- Tenancy and physical topology. Everything downstream keys on tenant_id and,
-- where the record is physically bound to a place, site_id.
--
-- Invariant: tenant_id is NOT NULL everywhere and is never read from a request
-- body. site_id is NOT NULL only on records that describe a physical thing at a
-- physical place.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE tenants (
    tenant_id   TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sites (
    site_id         TEXT NOT NULL,
    tenant_id       TEXT NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    name            TEXT NOT NULL,
    -- Rendering law: dates cross the site timezone, never the host's.
    timezone        TEXT NOT NULL,
    default_locale  TEXT NOT NULL DEFAULT 'he',
    site_tags       JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, site_id)
);

CREATE INDEX sites_name_idx ON sites (tenant_id, name);

-- Zones nest. A zone may hold zones, devices, and both at once.
CREATE TABLE zones (
    tenant_id   TEXT NOT NULL,
    zone_id     TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    parent_id   TEXT,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL,
    depth       INTEGER NOT NULL CHECK (depth >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, zone_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, parent_id) REFERENCES zones (tenant_id, zone_id) ON DELETE RESTRICT,
    -- A zone cannot be its own ancestor. The recursive traversal relies on this.
    CHECK (parent_id IS NULL OR parent_id <> zone_id)
);

CREATE INDEX zones_site_idx ON zones (tenant_id, site_id);
CREATE INDEX zones_parent_idx ON zones (tenant_id, parent_id);
CREATE INDEX zones_path_idx ON zones (tenant_id, path text_pattern_ops);

CREATE TABLE devices (
    tenant_id        TEXT NOT NULL,
    device_id        TEXT NOT NULL,
    site_id          TEXT NOT NULL,
    zone_id          TEXT,
    integration_id   TEXT,
    source_device_id TEXT,
    kind             TEXT NOT NULL CHECK (kind IN
                        ('camera', 'door', 'sensor', 'gate', 'alarm_panel', 'access_reader', 'other')),
    display_name     TEXT NOT NULL DEFAULT '',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    attributes       JSONB NOT NULL DEFAULT '{}'::jsonb,
    registered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, device_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, zone_id) REFERENCES zones (tenant_id, zone_id) ON DELETE RESTRICT
);

CREATE INDEX devices_site_idx ON devices (tenant_id, site_id);
CREATE INDEX devices_zone_idx ON devices (tenant_id, zone_id) WHERE zone_id IS NOT NULL;
CREATE INDEX devices_integration_idx ON devices (tenant_id, integration_id, source_device_id);

CREATE TABLE cameras (
    tenant_id          TEXT NOT NULL,
    camera_id          TEXT NOT NULL,
    device_id          TEXT NOT NULL,
    site_id            TEXT NOT NULL,
    source_camera_id   TEXT,
    display_name       TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'unknown',
    recording_state    TEXT NOT NULL DEFAULT 'unknown',
    width              INTEGER,
    height             INTEGER,
    low_light_capable  BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at       TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, camera_id),
    FOREIGN KEY (tenant_id, device_id) REFERENCES devices (tenant_id, device_id) ON DELETE CASCADE
);

CREATE INDEX cameras_device_idx ON cameras (tenant_id, device_id);
CREATE INDEX cameras_site_idx ON cameras (tenant_id, site_id);
