FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /raftgo ./cmd/raftgo

FROM scratch
COPY --from=build /raftgo /raftgo
EXPOSE 8080 9000
ENTRYPOINT ["/raftgo"]
