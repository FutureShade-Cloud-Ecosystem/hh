-- 0433: orders and tasks take their branch's org where they carry none (UX-95).
--
-- orders.org_id and tasks.org_id came from 0213 nullable with no default, and no order writer and not every
-- task writer stamped them. The writers stamp now (the branch's org, or the caller's), and this fills the rows
-- written before that: every orders and tasks row whose org_id IS NULL takes the org of its branch
-- (locations.org_id). A row whose branch carries no org stays NULL, and is counted in the notice below.
--
-- A task that was invisible to the tasks sync scopes because its org was NULL (the scopes filter tasks.org_id)
-- may sit under a device cursor, so each newly stamped PICK task draws a fresh pick_tasks seq and each
-- NEED_WEIGHT task a fresh weights seq, one set based draw per stream, in their present seq order, the way
-- 0422 re-draws a stream. The new seqs lie past every old one, so a device that already holds a higher cursor
-- still learns the row. Tasks of other types are not on a sync stream and keep their seq.
--
-- Invoices are not touched (the sync lane's SF-2p backfills invoices.org_id).
--
-- Idempotent: it writes only where org_id IS NULL, so a rerun stamps nothing and draws nothing. It deletes
-- nothing.
DO $$
DECLARE
    o_stamped integer;
    o_left    integer;
    t_stamped integer;
    t_left    integer;
    s         record;
    n         bigint;
    base      bigint;
BEGIN
    UPDATE orders o SET org_id = l.org_id
      FROM locations l
     WHERE o.org_id IS NULL AND l.id = o.branch_id AND l.org_id IS NOT NULL;
    GET DIAGNOSTICS o_stamped = ROW_COUNT;

    -- Tasks, stream by stream: stamp and re-draw the stream's seq in one statement.
    t_stamped := 0;
    FOR s IN SELECT * FROM (VALUES ('PICK', 'pick_tasks'), ('NEED_WEIGHT', 'weights')) AS v(task_type, stream) LOOP
        SELECT count(*) INTO n
          FROM tasks t JOIN locations l ON l.id = t.branch_id
         WHERE t.org_id IS NULL AND l.org_id IS NOT NULL AND t.task_type = s.task_type;
        CONTINUE WHEN n = 0;
        INSERT INTO scope_sequences (scope, next_value) VALUES (s.stream, n)
        ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + n
        RETURNING next_value - n INTO base;
        UPDATE tasks x SET org_id = r.org_id, seq = base + r.rn
          FROM (SELECT t.id, l.org_id,
                       row_number() OVER (ORDER BY t.seq NULLS FIRST, t.id) AS rn
                  FROM tasks t JOIN locations l ON l.id = t.branch_id
                 WHERE t.org_id IS NULL AND l.org_id IS NOT NULL AND t.task_type = s.task_type) r
         WHERE x.id = r.id;
        t_stamped := t_stamped + n;
    END LOOP;
    -- Every other task type: org only.
    UPDATE tasks t SET org_id = l.org_id
      FROM locations l
     WHERE t.org_id IS NULL AND l.id = t.branch_id AND l.org_id IS NOT NULL;
    GET DIAGNOSTICS n = ROW_COUNT;
    t_stamped := t_stamped + n;

    SELECT count(*) INTO o_left FROM orders WHERE org_id IS NULL;
    SELECT count(*) INTO t_left FROM tasks WHERE org_id IS NULL;
    RAISE NOTICE '0433: % order(s) and % task(s) took their branch''s org; % order(s) and % task(s) stay NULL (branch has no org, or no branch)',
        o_stamped, t_stamped, o_left, t_left;
END
$$;
