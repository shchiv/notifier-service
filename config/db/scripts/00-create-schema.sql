--
-- Tables
--
CREATE TABLE events
(
    id            UUID PRIMARY KEY,
    order_id      UUID      NOT NULL,
    sequence_num  INT       NOT NULL,
    customer_id   UUID      NOT NULL,
    payload       JSONB     NOT NULL,
    occurred_at   TIMESTAMP NOT NULL,

    created_at    TIMESTAMP NOT NULL DEFAULT NOW(),

    processed_at  TIMESTAMP,
    last_error_at TIMESTAMP,
    last_error    VARCHAR,
    failures_no   INT
);

CREATE UNIQUE INDEX unq_event_occurred_at ON events (order_id, occurred_at);
CREATE UNIQUE INDEX unq_event_order_sequence_num ON events (order_id, sequence_num);

COMMENT ON COLUMN events.processed_at IS 'Moment in time when event was successfully processed, or NULL otherwise';
COMMENT ON COLUMN events.last_error_at IS 'When the last error occurred, if any';
COMMENT ON COLUMN events.failures_no IS 'Number of failed processing attempts, if any';
COMMENT ON COLUMN events.sequence_num IS 'Synthetic field to check ordering (proper processing sequence) in order_projection';

CREATE TABLE customers
(
    customer_id UUID primary key ,
    webhook     VARCHAR,
    created_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMP -- value auto-populated by on update trigger
);

--
-- Views & required functions
--
CREATE FUNCTION calculateDurationSinceTillNow(since TIMESTAMP)
    RETURNS INT AS $$
SELECT EXTRACT(EPOCH FROM (now() - since));
$$ LANGUAGE sql VOLATILE;


CREATE OR REPLACE VIEW order_first_unprocessed_event AS
(
    SELECT ee.id, ee.customer_id, ee.order_id, ee.occurred_at
    FROM events ee,
         (
             SELECT e.order_id, MIN(e.occurred_at) first_non_processed
             FROM events e
             WHERE e.processed_at IS NULL
               AND EXISTS(
                 SELECT 1
                 FROM customers c
                 WHERE c.customer_id = e.customer_id
             )
             GROUP BY e.order_id
         ) q
    WHERE ee.order_id = q.order_id
      AND ee.occurred_at = q.first_non_processed
      AND (
            ee.last_error_at IS NULL OR (
            -- Simple Retry policy
                calculateDurationSinceTillNow(ee.last_error_at) > 5
            )
        )
);

CREATE OR REPLACE VIEW customer_processing_state AS
(
    SELECT
        q.*,
        GREATEST(q.last_processed_at, q.last_error_at) AS most_recent_event_change_at,
        GREATEST(c.created_at, c.modified_at) AS most_recent_customer_change_at
    FROM (
             SELECT e.customer_id, MAX(processed_at) last_processed_at, MAX(last_error_at) last_error_at
             FROM events e
             GROUP BY e.customer_id
         ) q
    LEFT JOIN customers c ON q.customer_id = c.customer_id
);

--
-- Triggers & trigger functions
--

CREATE OR REPLACE FUNCTION update_modifiedat_column()
    RETURNS TRIGGER AS $$
BEGIN
    NEW.modified_at = now();
    RETURN NEW;
END;
$$ language 'plpgsql' VOLATILE ;

CREATE TRIGGER on_b4_update_customer
    BEFORE UPDATE
    ON customers
    FOR EACH ROW
EXECUTE PROCEDURE  update_modifiedat_column();

CREATE FUNCTION unpack_event_payload()
    RETURNS TRIGGER AS
$$
BEGIN
    NEW.id = (NEW.payload::JSONB ->> 'id')::UUID;
    NEW.order_id = (NEW.payload::JSONB ->> 'order_id')::UUID;
    NEW.occurred_at = (NEW.payload::JSONB ->> 'occurred_at')::TIMESTAMP;
    NEW.customer_id = (NEW.payload::JSONB ->> 'customer_id')::UUID;
    NEW.sequence_num = (NEW.payload::JSONB ->> 'sequence_number')::INT;

    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER on_b4_insert_events
    BEFORE INSERT
    ON events
    FOR EACH ROW
EXECUTE PROCEDURE unpack_event_payload();


--
-- Business logic routines
--
CREATE OR REPLACE FUNCTION registerCustomerMock(customerHookURL VARCHAR, inactivityTimeout NUMERIC)
    RETURNS uuid AS $$
    DECLARE customerID uuid DEFAULT NULL;
BEGIN
        SELECT s.customer_id, customerHookURL
        FROM customer_processing_state s
        WHERE
            (s.last_processed_at IS NULL OR calculateDurationSinceTillNow(s.last_processed_at) > inactivityTimeout )
            AND (s.most_recent_customer_change_at IS NULL OR calculateDurationSinceTillNow(s.most_recent_customer_change_at) > inactivityTimeout)
        LIMIT 1 INTO customerID;

        IF customerID IS NOT NULL THEN
            WITH
                upsert (cid) AS
                    (
                        UPDATE customers c
                            SET webhook = customerHookURL
                            WHERE c.customer_id = customerID
                            RETURNING customer_id
                    )
            INSERT INTO customers (customer_id, webhook)
            SELECT customerID, customerHookURL
            WHERE NOT EXISTS (SELECT 1 FROM upsert up WHERE up.cid = customerID);
        END IF;

        RETURN customerID;
EXCEPTION
    WHEN unique_violation THEN
       RETURN NULL;
END;
$$ LANGUAGE 'plpgsql';


-- Must provide guarantees that the correspondent event record will be locked
-- and will not be returned within other sessions/transactions that execute this function
CREATE OR REPLACE FUNCTION lockAndGetEventForProcessing()
    RETURNS TABLE
            (
                event events,
                webhook varchar
            ) AS $$
BEGIN
    RETURN QUERY WITH cte AS (
        SELECT id FROM order_first_unprocessed_event
    )
                 SELECT e, (SELECT c.webhook FROM customers c WHERE c.customer_id = e.customer_id)
                 FROM events e, cte
                 WHERE e.id = cte.id
                     FOR UPDATE OF e SKIP LOCKED
                 LIMIT 1;
END;
$$ LANGUAGE plpgsql VOLATILE;


CREATE PROCEDURE updateEventDeliveryStatus(eventID uuid, err varchar) AS $$
DECLARE v_RowCount INT;
BEGIN
    UPDATE events e
    SET
        last_error = COALESCE(err, e.last_error),
        last_error_at =
            CASE
                WHEN err IS NULL THEN e.last_error_at
                ELSE NOW()
                END,
        failures_no =
            CASE
                WHEN err IS NULL THEN e.failures_no
                ELSE COALESCE(e.failures_no, 0) +1
                END,
        processed_at =
            CASE
                WHEN err IS NULL THEN NOW()
                ELSE e.processed_at
                END
    WHERE e.id = eventID;

    GET DIAGNOSTICS v_RowCount = ROW_COUNT;

    IF v_RowCount < 1 THEN
        RAISE SQLSTATE 'BRRRR'
        USING MESSAGE = format('Event with id "%s" does not exists', eventID);
    END IF;
END
$$ LANGUAGE plpgsql;
