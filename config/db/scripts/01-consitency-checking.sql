--
-- Solution consistence checking, it is not a part of production deployment
--
CREATE FUNCTION ensure_event_processing_consistency()
    RETURNS TRIGGER AS
    $$
DECLARE
    oldestUnprocessedEventID    uuid;
    oldestUnprocessedSeqNo      INT;

    recentProcessedEventID      uuid;
    recentProcessedSeqNo        INT;
    recentProcessedStatus       varchar;

    expectedEventID             uuid;
    expectedSeqNo               INT;
BEGIN
WITH
    recent_processed(order_id, recent_processed_event, recent_processed_seqno, recent_processing_status) AS(
        SELECT ee.order_id, ee.id, ee.sequence_num,
               CASE
                   WHEN ee.processed_at IS NULL THEN 'FAILED'
                   ELSE 'PROCESSED'
               END
        FROM events ee,
             (SELECT MAX(e.occurred_at) AS recent_processed_event
              FROM events e
              WHERE e.order_id = NEW.order_id
                AND (e.processed_at IS NOT NULL OR (e.processed_at IS NULL AND e.last_error_at IS NOT NULL))
             ) q
        WHERE ee.order_id = NEW.order_id AND ee.occurred_at = q.recent_processed_event
    ),
    oldest_unprocessed_occured_at(occurred_at) AS (
        SELECT MIN(ee.occurred_at)
        FROM events ee
        WHERE ee.order_id = NEW.order_id AND ee.processed_at IS NULL
    )
    SELECT e.id AS oldest_unprocessed_event,
           e.sequence_num AS oldest_unprocessed_seqno,
           rp.recent_processed_event,
           rp.recent_processed_seqno,
           rp.recent_processing_status
    FROM
        events e
            LEFT JOIN recent_processed rp on rp.order_id = e.order_id
            JOIN oldest_unprocessed_occured_at unp on e.occurred_at = unp.occurred_at
    WHERE
            e.order_id = NEW.order_id
        INTO oldestUnprocessedEventID, oldestUnprocessedSeqNo,
            recentProcessedEventID, recentProcessedSeqNo, recentProcessedStatus;

    CASE
        WHEN recentProcessedEventID IS NULL THEN
            -- IT is first time processing of first unprocessed event for that particular order
            expectedEventID = oldestUnprocessedEventID;
            expectedSeqNo = 0;

        WHEN recentProcessedStatus = 'FAILED' THEN
            -- Since recent is FAILED it should be retried
            expectedEventID = recentProcessedEventID;
            expectedSeqNo = recentProcessedSeqNo;

        WHEN recentProcessedStatus = 'PROCESSED' THEN
            expectedEventID = oldestUnprocessedEventID;
            expectedSeqNo = recentProcessedSeqNo +1;
        ELSE
            RAISE EXCEPTION 'Unhandled processing status: %', recentProcessedStatus;
    END CASE;

    IF expectedEventID != NEW.id OR expectedSeqNo != NEW.sequence_num THEN
        RAISE SQLSTATE 'ICERR'
            USING MESSAGE = format(E'Order [id:%s]: detected event processing inconsistency:\n' ||
                                   '[Old: event %s seqNo %s status %s]\n' ||
                                   '[New: event %s seqNo %s]\n' ||
                                   '[Act: event %s seqNo %s]\n' ||
                                   '[Exp: event %s seqNo %s].',
                                   NEW.order_id,
                                   recentProcessedEventID, recentProcessedSeqNo, recentProcessedStatus,
                                   oldestUnprocessedEventID, oldestUnprocessedSeqNo,
                                   NEW.id, NEW.sequence_num,
                                   expectedEventID, expectedSeqNo
                );
    END IF;

    RETURN NEW;
END
$$ language 'plpgsql';

CREATE TRIGGER on_b4_update_event
    BEFORE UPDATE
    ON events
    FOR EACH ROW
    EXECUTE PROCEDURE  ensure_event_processing_consistency();


-- It is not a mandatory table, but valuable to track ordering for the sake of correctness proof
CREATE TABLE order_projection
(
    order_id        UUID        PRIMARY KEY,
    sequence_num    INT         NOT NULL,
    modified_at     TIMESTAMP, -- value auto-populated by on update trigger
    created_at      TIMESTAMP   NOT NULL DEFAULT NOW()
);

CREATE FUNCTION ensure_order_processing_consistency()
    RETURNS TRIGGER AS
    $$
BEGIN
    CASE
        WHEN TG_OP = 'INSERT' THEN
            IF NEW.sequence_num != 0 THEN
                RAISE SQLSTATE 'ICERR'
                    USING MESSAGE = format('Order [id:%s]: first event has non zero sequence number [seqno:%s]', NEW.order_id, NEW.sequence_num);
            END IF;
        WHEN TG_OP = 'UPDATE' THEN
            IF NEW.sequence_num != OLD.sequence_num +1 THEN
                RAISE SQLSTATE 'ICERR'
                    USING MESSAGE = format('Order [id:%s]: event sequence is out of order: [Old:%s] [New:%s] [Expected: %s].',
                                           NEW.order_id, OLD.sequence_num, NEW.sequence_num, OLD.sequence_num +1);
            END IF;
        ELSE
            RAISE SQLSTATE 'ERROR'
                USING MESSAGE = format('Unsupported operation %', TG_OP);
    END CASE;

    RETURN NEW;
END
$$ language 'plpgsql';

CREATE TRIGGER on_b4_update_order_projection
    BEFORE UPDATE
    ON order_projection
    FOR EACH ROW EXECUTE PROCEDURE  update_modifiedat_column();

CREATE TRIGGER on_b4_insert_order_projection
    BEFORE INSERT
    ON order_projection
    FOR EACH ROW
    EXECUTE PROCEDURE ensure_order_processing_consistency();

CREATE TRIGGER on_after_update_order_projection
    BEFORE UPDATE
    ON order_projection
    FOR EACH ROW
    EXECUTE PROCEDURE ensure_order_processing_consistency();


CREATE PROCEDURE updateOrderProjection(orderID uuid, sequenceNo INT) AS $$
BEGIN
WITH
    upsert (oid) AS
        (
            UPDATE order_projection c
            SET sequence_num = sequenceNo
            WHERE order_id = orderID
                RETURNING order_id
            )
INSERT INTO order_projection (order_id, sequence_num)
SELECT orderID, sequenceNo
    WHERE NOT EXISTS (SELECT 1 FROM upsert up WHERE up.oid = orderID);
END;
$$ LANGUAGE 'plpgsql';
