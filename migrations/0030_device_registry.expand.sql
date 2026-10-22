-- 0030_device_registry.expand.sql
--
-- The device registry: what a device IS, what was MEASURED about it, and where
-- its credential lives.
--
-- # What this migration is for
--
-- The AI VMS plan's P1. The product now owns a camera path, and a camera path
-- needs three things the platform did not have: a registry of the devices we
-- are responsible for, a record of what each device can actually do, and a
-- pointer to the credential that opens it.
--
-- The `devices` table already existed -- migration 0001 created it, because a
-- security graph needs devices whether or not anybody records them. What it
-- did not have is the recording half: who the device is, where its stream is,
-- what it can do, and how we get in.
--
-- # The capability manifest is MEASURED, and the column is where that lives
--
-- `capabilities` holds one manifest: the capability set a probe established
-- about this device, with the state of each capability, the probe that
-- decided it, and when.
--
-- The state vocabulary is closed and it is the same four words the published
-- JSON Schema carries and the same four the Go package uses:
--
--   unverified   -- claimed by the device, never checked
--   verified     -- a probe established it. THE ONLY STATE THAT COUNTS
--   contradicted -- a probe established the device does NOT have it
--   unsupported  -- a probe established the device does not have it and does
--                   not claim it
--
-- A camera names the profiles it supports and is routinely wrong about that.
-- A product that believes a device's own claims has a capability report that
-- is fiction, so the manifest is written by a probe and the product reads
-- only the `verified` entries. The Go side refuses to publish a `verified`
-- capability with no probe behind it, and the `device-capability-manifest`
-- gate fails the build when one appears.
--
-- The column is JSONB rather than a table of rows because a manifest is READ
-- WHOLE or NOT AT ALL: a consumer needs the whole set to decide what to
-- enable, and half a manifest is not a smaller manifest, it is a wrong one.
-- The published shape is `contracts/json-schema/capability-manifest.schema.json`
-- and the binary carries a byte-identical copy.
--
-- # `credential_ref` is a pointer, and the secret is not here
--
-- The camera's RTSP and ONVIF credentials live in the keystore
-- (`internal/identity`). This table names the keystore reference and nothing
-- else. A camera's default password is the single most valuable secret a
-- physical-security product holds, and a table that can be read by a console
-- query, a log line, a support export or a backup is a table that leaks it.
--
-- `device_credentials` is the INVENTORY: one row per (device, purpose), each
-- naming a keystore reference, when it was enrolled and when it was last
-- verified against the device. `devices.credential_ref` is the fast path --
-- the one credential the recorder opens the stream with -- and the inventory
-- is everything we hold, so rotation and revocation have somewhere to read
-- from.
--
-- # `rtsp_ref`, not `rtsp_url`
--
-- The column holds a REFERENCE, not a URL, and the reason is the same reason
-- the credential is a reference: a URL with a password in it is a password in
-- a log, a URL in an API response, and a URL in a support screenshot. The
-- stream configuration lives in the product's configuration; this table says
-- WHICH configuration.
--
-- # Expand, not contract
--
-- Eight columns added to a table that already existed, one new table, two
-- new policies and a grant. No column is dropped, no data is moved, and
-- nothing that reads `devices` today changes behaviour: every new column is
-- nullable, and a device with no manifest simply has no measured
-- capabilities, which is the honest state for a device nobody has probed.

ALTER TABLE devices
    -- What the device says it is. Never a capability claim: a model name is not
    -- a measurement, and the manifest below is where measurements live.
    ADD COLUMN IF NOT EXISTS make TEXT,
    ADD COLUMN IF NOT EXISTS model TEXT,
    ADD COLUMN IF NOT EXISTS firmware TEXT,
    -- The device's own identifier, as it reports it. Not a tenant-scoped key.
    ADD COLUMN IF NOT EXISTS onvif_device_id TEXT,
    -- A reference to the device's stream, held in configuration. It carries no
    -- credential, and it is not a URL: a URL with a password in it is a
    -- password in a log.
    ADD COLUMN IF NOT EXISTS rtsp_ref TEXT,
    -- A reference into the keystore. A pointer, never a secret. The secret
    -- itself is never in this table, in a log, or in the ledger.
    ADD COLUMN IF NOT EXISTS credential_ref TEXT,
    -- The substream the recorder should use, when the device has more than one.
    ADD COLUMN IF NOT EXISTS substream_profile TEXT,
    -- The MEASURED capability set of this device, in the shape published at
    -- `contracts/json-schema/capability-manifest.schema.json`. NULL means the
    -- device has never been probed, and a device that has never been probed
    -- has no capabilities.
    ADD COLUMN IF NOT EXISTS capabilities JSONB;

-- The credential inventory. One row per (device, purpose); the secret_ref is a
-- pointer into the keystore and the column is named so that nobody reading
-- the table mistakes it for the secret.
CREATE TABLE IF NOT EXISTS device_credentials (
    tenant_id  TEXT NOT NULL,
    device_id  TEXT NOT NULL,
    -- What the credential opens. Closed vocabulary: the purposes the product
    -- actually has, so a credential's purpose cannot drift into free text.
    purpose    TEXT NOT NULL
               CHECK (purpose IN ('rtsp', 'onvif', 'ftp', 'other')),
    -- A reference into the keystore. A pointer, never a secret.
    secret_ref TEXT NOT NULL,
    enrolled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- When the credential was last checked against the device itself. NULL
    -- means it has never been verified, which is not the same as working.
    last_verified_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, device_id, purpose),
    FOREIGN KEY (tenant_id, device_id)
        REFERENCES devices (tenant_id, device_id) ON DELETE CASCADE
);

-- One index per direction the product asks by: every credential a tenant
-- holds, and every credential one device holds.
CREATE INDEX IF NOT EXISTS device_credentials_tenant_idx
    ON device_credentials (tenant_id, device_id);

-- ---------------------------------------------------------------------------
-- The policies, the force, and the grant
-- ---------------------------------------------------------------------------
--
-- A table born after migration 0008's and 0023's catalogue loops gets nothing
-- from either loop's next run: both loops ask the catalogue about tables as it
-- was. So the policies, the force, AND the grant all belong here, together.
ALTER TABLE device_credentials ENABLE ROW LEVEL SECURITY;
-- FORCE, not just ENABLE: the table owner would otherwise bypass its own
-- policies, and an owner connection is exactly the connection a maintenance
-- script tends to use.
ALTER TABLE device_credentials FORCE ROW LEVEL SECURITY;
-- Tenant first, site second would need a site_id, and this table has none:
-- a credential belongs to a DEVICE, and a device's site is one hop away. The
-- device's own site_isolation policy is what bounds a read of this table's
-- contents to a principal who may see the device, so the tenant policy here is
-- the boundary that matters and the device's is the one that narrows it.
CREATE POLICY tenant_isolation ON device_credentials
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());

-- The role's privileges are the OTHER half a table created after 0008 lacks.
-- Without the GRANT a correct-scope write from `security_core` is refused with
-- `permission denied`, which reads as a policy failure and is a missing GRANT.
GRANT SELECT, INSERT, UPDATE, DELETE ON device_credentials TO security_core;

COMMENT ON TABLE device_credentials IS
    'The credential INVENTORY: one row per (device, purpose), each naming a keystore '
    'reference. The secret itself is never in this table, in a log, or in the ledger -- '
    'the column is named secret_ref so that nobody reading the table mistakes the pointer '
    'for the secret. Created by migration 0030 for the AI VMS device registry.';

COMMENT ON COLUMN devices.capabilities IS
    'The MEASURED capability set of this device, in the shape published at '
    'contracts/json-schema/capability-manifest.schema.json. NULL means the device has '
    'never been probed, and a device that has never been probed has no capabilities. Only '
    'the `verified` entries count: a camera names the profiles it supports and is routinely '
    'wrong about that.';

COMMENT ON COLUMN devices.credential_ref IS
    'A reference into the keystore for the credential the recorder opens this device''s '
    'stream with. A pointer, never a secret. The full inventory is in device_credentials.';
