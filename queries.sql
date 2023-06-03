--
--  INSERT TEST DATA
--

INSERT INTO events (payload)
VALUES
    ('{"sequence_number": 0, "id": "1edd415b-1dcf-6729-bf19-0be6c8a9c2fc", "type": "driver_assigned", "order_id": "72da9110-d416-11ed-afa1-0242ac120002", "customer_id": "72da9110-d416-11ed-afa1-0242ac120002", "occurred_at": "2023-04-12 16:33:55Z"}'),
    ('{"sequence_number": 1, "id": "ff99a978-d417-11ed-afa1-0242ac120002", "type": "driver_assigned2", "order_id": "72da9110-d416-11ed-afa1-0242ac120002", "customer_id": "72da9110-d416-11ed-afa1-0242ac120002", "occurred_at": "2023-04-12 16:34:55Z"}'),

    ('{"sequence_number": 0, "id": "1428c608-d418-11ed-afa1-0242ac120002", "type": "driver_assigned", "order_id": "2030c68a-d418-11ed-afa1-0242ac120002", "customer_id": "724eaa54-d418-11ed-afa1-0242ac120002", "occurred_at": "2023-04-12 16:33:55Z"}'),
    ('{"sequence_number": 1, "id": "18ed3c28-d418-11ed-afa1-0242ac120002", "type": "driver_assigned2", "order_id": "2030c68a-d418-11ed-afa1-0242ac120002", "customer_id": "724eaa54-d418-11ed-afa1-0242ac120002", "occurred_at": "2023-04-12 16:34:55Z"}');


COMMIT;


--
--  Query example: UPSERT order_projection
--

select * from order_projection where order_id = '72da9110-d416-11ed-afa1-0242ac120002';
INSERT INTO order_projection (order_id, sequence_num)
VALUES (
           '72da9110-d416-11ed-afa1-0242ac120002', 0
       )
ON CONFLICT (order_id) DO UPDATE
    SET sequence_num = 1;


--
-- REGISTER notification handler
--
BEGIN ISOLATION LEVEL READ COMMITTED;

SELECT registerCustomerMock('http://hookX', 120);

-- GET first available event for delivery
BEGIN ISOLATION LEVEL READ COMMITTED;
SELECT (pe.event).id, (pe.event).customer_id, pe.webhook FROM  lockAndGetEventForProcessing() pe;

ROLLBACK;

--
-- Check locks
--

SELECT
    CASE
        WHEN q.table_name = 'customers' THEN (SELECT c.customer_id FROM customers c WHERE c.ctid = q.locked_row )
        WHEN q.table_name = 'events' THEN (SELECT e.id FROM events e WHERE e.ctid = q.locked_row )
        END AS pk,
       q.*
FROM (SELECT 'customers' AS table_name, * FROM pgrowlocks('customers')
      UNION ALL
      SELECT 'events', *
      FROM pgrowlocks('events')
) q;


select pid, nspname, relname,
       (CASE c.relkind
            WHEN 'r' THEN 'table'
            WHEN 'i' THEN 'index'
            WHEN 'S' THEN 'sequence'
            WHEN 't' THEN 'toast table'
            WHEN 'v' THEN 'view'
            WHEN 'm' THEN 'materialized view'
            WHEn 'c' THEN 'composite'
            WHEN 'f' THEN 'foreign table'
            WHEn 'p' THEN 'partitioned table'
            WHEN 'I' THEN 'partitioned index'
--             ELSE c.relkind
           END
           ) as relkind,
       l.locktype,
       CASE l.locktype
           WHEN 'relation' THEN relation::regclass::text
           WHEN 'transactionid' THEN transactionid::text
           WHEN 'tuple' THEN relation::regclass::text||':'||tuple::text
           END AS lockid,
       mode, granted, fastpath
from pg_locks l
         left join pg_class c on (l.relation = c.oid)
         left join pg_namespace nsp on (c.relnamespace = nsp.oid)
WHERE locktype in ('relation','transactionid','tuple')
  AND (locktype != 'relation' OR relation = 'customers'::regclass OR relation = 'events'::regclass);
--WHERE relname LIKE 'customer%' OR relname LIKE 'event%';
