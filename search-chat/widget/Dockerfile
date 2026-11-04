# 构建：docker build -t search-chat .
# 运行：docker run -p 8080:8080 --env-file .env search-chat
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /search-chat-server ./cmd/search-chat-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
ENV TZ=Asia/Shanghai PORT=8080
COPY --from=build /search-chat-server /usr/local/bin/search-chat-server
USER app
EXPOSE 8080
ENTRYPOINT ["search-chat-server"]
