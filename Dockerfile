FROM golang:1.19 AS builder
ENV GO111MODULE=on \
    CGO_ENABLE=0\
    GOOS=linux\
    GOARCH=amd64

WORKDIR /build
COPY . .
RUN go build -o onett .

FROM busybox
ENV URL_STRING=""
COPY --from=builder /build/onett /
RUN mkdir /onett_logs
EXPOSE 8080
ENTRYPOINT ["/onett"]
