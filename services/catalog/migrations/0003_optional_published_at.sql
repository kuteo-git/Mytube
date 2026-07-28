-- Scanned videos have no upload date.
--
-- Flat playlist listings omit it, and the previous default of "now" made every
-- freshly scanned video read as published a minute ago. Allowing NULL lets the
-- UI say nothing instead of saying something false.

SET search_path = catalog;

ALTER TABLE videos ALTER COLUMN published_at DROP NOT NULL;
ALTER TABLE videos ALTER COLUMN view_count DROP DEFAULT;
ALTER TABLE videos ALTER COLUMN view_count DROP NOT NULL;
