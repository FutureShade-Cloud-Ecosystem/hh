-- 0455: the whole year export's batch word (W3 AX-1 item 3, audit GL-08).
-- gl_export_batches.month's check refused everything a batch can be but one month: the whole year
-- export (the desk's Whole year pick, month 0) read every row of the year and then died recording
-- its batch. 0 joins the check as the wider than a month word, the honest record for a year pick or
-- a quarter pick; it was never a calendar month and names no month.

ALTER TABLE gl_export_batches DROP CONSTRAINT gl_export_batches_month_check;
ALTER TABLE gl_export_batches ADD CONSTRAINT gl_export_batches_month_check CHECK (month BETWEEN 0 AND 12);
