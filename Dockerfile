FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /afeehy-hour .

FROM alpine:3.20
RUN adduser -D -u 10001 afeehy && mkdir -p /data && chown afeehy /data
WORKDIR /data
COPY --from=build /afeehy-hour /usr/local/bin/afeehy-hour
ENV PORT=8080 DATA_FILE=/data/afeehy-data.json
# Set HOST_KEY and EVENT_CODE as secrets. Set TRUST_PROXY=1 when a proxy or tunnel sits in front.
VOLUME /data
EXPOSE 8080
USER afeehy
CMD ["afeehy-hour"]
