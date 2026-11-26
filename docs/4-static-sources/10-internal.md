# Internal path source

The internal path source allows a path to read the stream of another path of the
same MediaMTX instance, without opening any network connection and without
re-encoding the media. It is useful to expose the same stream through multiple
paths with different access permissions or settings.

```yml
paths:
  source:
  target:
    source: internal://source
```

The stream published (or pulled) on path `source` is made available on path
`target` as well. A single source can be reused by any number of target paths.

## Properties

- No local network connection is opened: the target path reads the source
  stream directly inside the instance. In particular, no RTSP/RTMP/SRT/WebRTC
  session is created on the source path.
- Media units and timestamps are passed through untouched; there is no
  re-encoding. Only H264 video and MPEG-4 Audio (AAC) are supported; any other
  codec makes the target path unable to become available.
- The internal reader bypasses authentication on the source path
  (`SkipAuth`), while readers of the target path still authenticate against the
  target path configuration. This makes it possible to grant different access
  permissions to paths sharing the same media.
- If the source path is not online yet, the target path waits and attaches
  automatically when the source becomes available. If the source disconnects,
  the target path becomes unavailable and re-attaches automatically once the
  source comes back.
- Internal readers are hidden: they do not appear in the `readers` list of the
  source path returned by the API.

## Restrictions

- The source path must be a regular (non-regexp) path name.
- A path cannot use itself as a source, and configurations that form a cycle
  (`a -> b -> a`, including longer chains) are rejected at startup and on
  configuration reload.
