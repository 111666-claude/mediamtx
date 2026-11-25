# RTMP-specific features

RTMP is a protocol that can be used for publishing and reading streams. Regarding specific tasks, check out [Publish with RTMP clients](../3-publish/05-rtmp.md) and [Read with RTMP clients](../5-read/05-rtmp.md). Features in this page are shared among both tasks.

## Encryption

RTMP connections can be encrypted by using the secure protocol variant (RTMPS). A TLS certificate is needed and can be generated with OpenSSL:

```yml
openssl genrsa -out server.key 2048
openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650
```

Edit mediamtx.yml and set the `rtmpEncryption`, `rtmpServerKey` and `rtmpServerCert` parameters:

```yml
rtmpEncryption: optional
rtmpServerKey: server.key
rtmpServerCert: server.crt
```

Streams can be published and read with the rtmps scheme and the 1937 port:

```
rtmps://localhost:1937/...
```

Be aware that RTMPS is currently unsupported by most major players. However, you can use a local _MediaMTX_ instance to decrypt streams before reading them, or alternatively a proxy like [stunnel](https://www.stunnel.org) or [nginx](https://nginx.org/). For instance, you can launch a local _MediaMTX_ instance with this configuration:

```yml
paths:
  decrypted:
    source: rtmps://original-stream
```

And then read `rtmp://localhost/decrypted` instead of `rtmps://original-stream`.

## Limiting unauthenticated connections

Every accepted RTMP/RTMPS connection consumes a socket and goroutine, and slow clients can hold resources while they perform the handshake or wait for authentication. The `rtmpMaxUnauthenticatedConnections` parameter bounds how many of these not-yet-authorized connections can be open at the same time:

```yml
rtmpMaxUnauthenticatedConnections: 100
```

Admission is enforced before the handshake, protocol parsing and the `runOnConnect` hook. When the limit is reached, new connections are closed immediately. As soon as a connection authenticates successfully, or when it fails, times out or disconnects, its slot is released. Already authorized publishers and readers are therefore never limited or disconnected by this parameter, and each of RTMP and RTMPS gets its own independent budget.

The value can be reloaded at runtime without dropping active connections. The default value is `0`, which means no limit and preserves the standard behavior.
