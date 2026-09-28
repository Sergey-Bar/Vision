-- 0011_source_clock_state.expand.sql
--
-- Allow device_clocks to hold a source system's clock as well as a device's.
--
-- A VMS 40 seconds fast is not one bad camera, it is every event that system
-- produced. The drift belongs in the same table the rules read every other clock
-- from, not in a second table with one row, or a site with a fast VMS ends up
-- with a number nobody reads.
--
-- The id is namespaced with a prefix rather than being a separate column, so
-- "is this a device or a source" is a fact about the id and there is no second
-- thing to keep consistent.

ALTER TABLE device_clocks
    ADD CONSTRAINT device_clocks_id_qualified
    CHECK (device_id = '' OR device_id NOT LIKE '%:%' OR device_id LIKE 'source:%');