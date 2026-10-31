-- 0469: job_id on quotes, orders and invoices (wave 3 JA-2).
--
-- A document carries the job it was raised for. The column is NULLABLE and NULL is the common
-- case: a quote, an order or an invoice written with no job in view belongs to the account as a
-- whole, and null is not "no job in particular" spelled wrong, it is the account's own document.
-- A document that names a job names one of ITS CUSTOMER's jobs (the service fence refuses
-- another customer's job, a closed job and a job outside the caller's reach, in words), so the
-- foreign key never has to be the fence: it is the last line of defence, and the module boundary
-- holds above it, each table written only by its own module (quote writes quotes, quote's
-- conversion writes orders, pos writes its ring's orders and invoices, billing writes invoices).
--
-- ON DELETE RESTRICT, the rule every document reference on these tables already carries
-- (customer_id, branch_id). A job is closed, never deleted: no door deletes one, and a customer
-- delete is itself restricted by any document that names the customer. The day a dealer needs a
-- job gone, its documents are moved first, the same as its branch's would be.
--
-- THE INDEXES ARE PARTIAL, on job_id IS NOT NULL, the shape idx_orders_project already carries:
-- most rows name no job, and a full index would hold a copy of the ledger to answer a question
-- almost nobody asks. JA-3 (exposure by job) and JA-4 (the statement and the aging by job) read
-- documents BY their job; these are the indexes those reads need.
--
-- Owner: the modules that own each table (quote, order, pos, billing), unchanged by this file.

ALTER TABLE quotes   ADD COLUMN IF NOT EXISTS job_id UUID REFERENCES customer_jobs(id) ON DELETE RESTRICT;
ALTER TABLE orders   ADD COLUMN IF NOT EXISTS job_id UUID REFERENCES customer_jobs(id) ON DELETE RESTRICT;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS job_id UUID REFERENCES customer_jobs(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_quotes_job   ON quotes(job_id)   WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_job   ON orders(job_id)   WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_invoices_job ON invoices(job_id) WHERE job_id IS NOT NULL;
