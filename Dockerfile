#
# Builder
#
ARG GCFLAGS=-l=0
FROM golang:1.20.2-alpine3.17 as builder
# GCO must be used to build kafka-consumer that uses github.com/confluentinc/confluent-kafka-go
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOPROXY=https://proxy.golang.org
ENV TAGS=musl

WORKDIR /app

RUN apk --update --no-cache upgrade \
    && apk add --no-cache go gcc g++ make

COPY go.* /app/
RUN go mod download

COPY . .
RUN make all

#
# Base image
#
FROM alpine:3.17 as base-image
WORKDIR /usr/local/bin

RUN apk --update --no-cache upgrade \
    && apk add --no-cache su-exec curl jq

RUN /usr/bin/getent passwd user || adduser -D -s /bin/sh -h /home/user user user  \
    && chown user:user -R /home/user \
    && chown user:user -R /usr/local/bin/

COPY ./docker/entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

#
# Kafka producer
#
FROM base-image as kafka-producer
COPY --from=builder /app/bin/producer /usr/local/bin/
CMD ["./producer"]


#
# Kafka consumer
#
FROM base-image as kafka-consumer
#RUN apk add --no-cache librdkafka
COPY --from=builder /app/bin/consumer /usr/local/bin/
CMD ["./consumer"]


#
# Notifier
#
FROM base-image as notifier
COPY --from=builder /app/bin/notifier /usr/local/bin/
CMD ["./notifier"]


#
# Catcher
#
FROM base-image as catcher
COPY --from=builder /app/bin/catcher /usr/local/bin/
CMD ["./catcher"]