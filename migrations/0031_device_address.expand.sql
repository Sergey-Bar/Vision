-- 0031_device_address.expand.sql
--
-- Where a device IS.
--
-- Migration 0030 gave the registry everything about a device EXCEPT how to
-- reach it, because the device's address was going to live in the site's
-- configuration alongside the stream reference. It turns out that is the wrong
-- place for it, and this migration says why.
--
-- # Why the address is in the table
--
-- The probe MEASURES a device. To measure it, the product has to know where
-- it is. A probe that takes its address from the request is a probe that can
-- be pointed at any address by any caller, and the manifest it writes would
-- then describe a device that is not the one the row is about. The registry
-- row and the measurement have to be about the SAME device, so the address
-- that the measurement is taken against has to be the address the row carries.
--
-- The alternative — a separate table of addresses — is a second reader for
-- one fact and a second place to keep in sync.
--
-- # The address is not a secret
--
-- It is a host and a port. It carries no credential, and the column is
-- spelled `onvif_base_url` rather than `url` because it names the BASE the
-- control service hangs off, not a complete request. A device that needs a
-- credential has one, and the credential lives in the keystore and is named
-- by `devices.credential_ref` — never here, never in a log, never in the
-- ledger.
--
-- # An expand
--
-- One nullable column, no backfill, no data moved, nothing dropped. A device
-- registered before this migration has a NULL address, which means the
-- product does not know where it is, and the probe says so by name rather
-- than guessing an address and measuring whatever answered.

ALTER TABLE devices
    -- The device's ONVIF base address. NULL means the product does not know
    -- where the device is.
    ADD COLUMN IF NOT EXISTS onvif_base_url TEXT;

COMMENT ON COLUMN devices.onvif_base_url IS
    'The base address of the device''s ONVIF control service, e.g. http://192.0.2.10:80. '
    'A host and a port and nothing else: the credential is in the keystore and is named by '
    'credential_ref, never here. NULL means the product does not know where the device is, '
    'and the probe refuses rather than guessing an address and measuring whatever answered.';
