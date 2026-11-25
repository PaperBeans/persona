-- Host correlation for managers: keep the last machine_uid reported in manager.hello alongside host/os/arch/deploy/version.
ALTER TABLE bus_identities
    ADD COLUMN IF NOT EXISTS machine_uid text NOT NULL DEFAULT '';
