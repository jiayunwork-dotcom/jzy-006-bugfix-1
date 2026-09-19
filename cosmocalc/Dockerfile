# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /bin/server ./cmd/server

# ---- runtime stage ----
FROM alpine:3.20
RUN adduser -D -u 10001 appuser
COPY --from=build /bin/server /bin/server
USER appuser
EXPOSE 8080
ENTRYPOINT ["/bin/server"]
