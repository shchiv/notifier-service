# notifier-service

The system process customer orders (multiple orders per customer), and each order has a current state that is changed 
in time by the progress of handling particular orders. Particular customers can register their webhook callbacks 
(notification handlers) to receive order state-changed notifications (one per customer). 
Order state-changed events come for processing via Kafka messages; one message contains one event.

## Requirements
1. Order state-changed notifications within one order should be delivered in the same order
as they were triggered (**req1: ordering**).

2. Potential performance degradation of one or more notification handlers must not affect the performance of the 
rest notification handlers (**req2: unaffected performance**).

3. Order state-changed notifications that failed to deliver must be attempted to re-deliver with respect
to the retry policy (**req3: retried delivery**).

4. Horizontal scalability should mitigate (reduce) delivery latency for order state-changed 
notifications (**req4: horizontal scalability**).    

## Test Scenario for 'unaffected performance' requirement

### Modes description:
- `Nightmare` mode, which provides about 33% of probability of delay or exception (500 status code) will be returned.
- `Hell` mode, which always returns an exception (500 status code).

### Start configuration:
- two notification handlers with `nightmare` mode, a possible delay of 500-1000 ms, and status code 500, with a probability about 33% of one of the assaults will be executed.
- one notification handler will be looped by two modes, starting with a `nightmare` - which will last 1 minute, switch to `hell` - which will last 3 minutes, and return to the nightmare again, so these modes will change each other and again and again.
- one notification handler without any modes, so it will work without expected delays or exceptions.

To be sure that external customer issues are not affecting the performance,
we will start a few instances (described in [Start configuration section](#Start configuration)),
and the additional catcher, which will start with `nightmare` but changed to `hell`, won't handle any requests, 
during this time other catchers will continue to work, and won't be affected.
After the period when `hell` will be finished, everything should be backed to a normal state on catcher, others should continue to work as before.
and we should have the same performance as before the whole experiment.

## psql (cli client) on MAC
```shell
brew install libpq
brew link --force libpq

PGPASSWORD=password psql -U user -h localhost local    
```

## Kafka
http://localhost:8080

## Prometheus
Check registered targets: http://localhost:9090/targets?search=&scrapePool

## Grafana
http://localhost:3000

## Thoughts
1. Kafka only provides a total order over messages within a partition, not between different partitions in a topic.
2. In order to decrease load on DB in kafka consumer (ingestor), better to use idempotent producers,
   otherwise there are will be attempts to ingest duplicated data that cause extra DB load. 
3. Why consumer is implemented as it is: since it is a very sensitive part from the sake of performance, 
   it MUST be implemented in the most efficient way to minimize any impact.
   And that means use batch processing for DB ingestion as awell as for Kafka message commits. 
   
   There is some materials about efficient batch inserts for PostgresSQL:
      - [How do I batch sql statements with package database/sql](https://stackoverflow.com/questions/12486436/how-do-i-batch-sql-statements-with-package-database-sql)
      - [Choosing Between the pgx and database/sql Interfaces](https://github.com/jackc/pgx#choosing-between-the-pgx-and-databasesql-interfaces): The pgx interface is faster. Many PostgreSQL specific features such as LISTEN / NOTIFY and COPY
        are not available through the database/sql interface.
      - [Bulk INSERT in Postgres in GO using pgx](https://stackoverflow.com/questions/70823061/bulk-insert-in-postgres-in-go-using-pgx)
4. How does the customer simulator(catcher) choose what customer id to handle (self-registration):
   **Here is a very hard requirement: the only one simulator must serve particular customer**, that means that 
   if there are more simulators are up and running then customers, some simulators should be in idle state 
   (very similar to Kafka consumers, in case more consumer than partitions exists).
   
   **Implementation details:** 
   There are should be a record in `customers` table for each customer that is/was serving by a simulator.

   <br/>Registration logic should take the first `customer id` that has unprocessed ( within the processing window) events,
   and cover the following cases:
      - **case1**: insert `customer id` record if correspondent customer has never been served (customer table does not contain record for particular `customer id`);
      - **case2**: update existing  record if `customer id` was served before, but on whatever reason previously registered simulator does not handle events within processing window;
      - **case3**: handle collisions to avoid situation when newly and previously registered agents will serve the same `customer id`. 

   <br>Agent registration should be implemented as `upsert` query, that covers **case1** and **case2**, but **case3** should be covered separately.
   PostgresSQL supports kind of [DO UPDATE SET](https://www.postgresql.org/docs/current/sql-insert.html#SQL-ON-CONFLICT),
   but usage of that syntax causes [LOST UPDATE](https://dzone.com/articles/what-is-a-lost-update-in-database-systems).   
   The better solution is [Writeable CTE](https://stackoverflow.com/questions/1109061/insert-on-duplicate-update-in-postgresql/8702291#8702291).
   In accordance with **case1**, the correspondent customer record may not exist, what makes it impossible to use [Row Level Lock FOR UPDATE](https://postgreshelp.com/postgresql-locks/).
   As a result WCTE registration query can utilize 'SKIPP LOCKED' and it will fail with "duplicate key value violates unique constraint 'customers_pkey'"
   for all simulators except the first one that the same time will be trying to register themselves as servants for the same customer id,
   and those simulators will need to re-try registration till query will be succeeded with zero affected records (no suitable customer ids to serve) 
   or with one affected record (successful registration).

   <br/>**TBD case3**

5. ??
## TODO

1. Handle re-try messages in consumer, so far the entire batch of messages just ignored:
2. Ensure that consumer handles re-balancing properly
3. Optimize Queries to achieve better performance

## Materials

1. [Supercharge your Kafka Clusters with Consumer Best Practices](https://www.groundcover.com/blog/kafka-consumer-best-practices)
2. [Kafka Idempotent Producer](https://www.linkedin.com/pulse/kafka-idempotent-producer-rob-golder/) 
3. PostgreSQL:
   1. Locks in PostgreSQL: Row-level locks https://postgrespro.com/blog/pgsql/5968005
4. Kafka metrics:
   1. Kafka prometheus exporters https://alex.dzyoba.com/blog/jmx-exporter/
   2. https://www.metricfire.com/blog/kafka-performance-monitoring-metrics/
   3. https://docs.confluent.io/platform/current/kafka/monitoring.html
   4. [Kafka Metrics: Optimize the Performance of Kafka Applications](https://hevodata.com/learn/kafka-metrics/)
   5. [Monitor Kafka Consumer Group Latency with Kafka Lag Exporter](https://www.lightbend.com/blog/monitor-kafka-consumer-group-latency-with-kafka-lag-exporter)
5. Prometheus:
   1. [Practical monitoring with Prometheus](https://blog.softwaremill.com/practical-monitoring-with-prometheus-ee09a1dd5527)
   2. [A Deep Dive Into the Four Types of Prometheus Metrics](https://www.timescale.com/blog/four-types-prometheus-metrics-to-collect/)
   3. Custom metrics exporter (Golang):
      1. [Proper HTTP shutdown in Go](https://medium.com/@mokiat/proper-http-shutdown-in-go-bd3bfaade0f2)
      2. https://github.com/prometheus/client_golang/blob/main/examples/middleware/httpmiddleware/httpmiddleware.go
      3. https://github.com/prometheus/client_golang/blob/main/prometheus/examples_test.go#L183
   4. ??
6. Orchestration:
   1. https://www.confluent.io/blog/monitor-kafka-clusters-with-prometheus-grafana-and-confluent/?utm_medium=sem&utm_source=google&utm_campaign=ch.sem_br.nonbrand_tp.prs_tgt.dsa_mt.dsa_rgn.namer_lng.eng_dv.all_con.blog&utm_term=&creative=&device=c&placement=&gclid=CjwKCAjw_YShBhAiEiwAMomsEGP14FygTxX-JT5BTUWZDmhGjJ2MqXbAawgPvl1w2-2bqa6suZ7HzBoC09cQAvD_BwE
   2. https://github.com/streamthoughts/kafka-monitoring-stack-docker-compose
   3. https://github.com/jeanlouisboudart/kafka-platform-prometheus
7. ??
