FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY docs ./docs
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /keystoner ./cmd/keystoner
FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /keystoner /app/keystoner
COPY catalog /app/catalog
USER app
ENV PORT=8080
EXPOSE 8080
CMD ["/app/keystoner"]
