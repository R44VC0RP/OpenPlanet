FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/game .

FROM alpine:3.22
RUN adduser -D -u 10001 game
WORKDIR /app
COPY --from=build /out/game /app/game
USER game
EXPOSE 8081
ENTRYPOINT ["/app/game"]
