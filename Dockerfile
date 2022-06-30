FROM golang:1.18.3 AS builder
ADD main.go go.mod go.sum /go/src/cpburner/
RUN cd /go/src/cpburner && go build .

FROM ubuntu:latest
COPY --from=builder /go/src/cpburner/cpburner /usr/local/bin/cpburner
CMD ["cpburner", "-h"]