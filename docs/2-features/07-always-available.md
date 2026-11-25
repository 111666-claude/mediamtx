# Always-available

When the publisher or source of a stream is offline, the server can be configured to fill gaps in the stream with an offline segment that is played on repeat until a publisher comes back online. This allows readers to stay connected regardless of the state of the stream. The offline segment and online stream are concatenated without re-encoding any frame, using the original codec.

This feature can be enabled by toggling the `alwaysAvailable` flag and filling `alwaysAvailableTracks`:

```yml
paths:
  mypath:
    alwaysAvailable: true
    alwaysAvailableTracks:
      # Available values are: AV1, VP9, H265, H264, Opus, MPEG4Audio, G711, LPCM
      - codec: H264
        # in case of MPEG4Audio, G711, LPCM, sampleRate and ChannelCount must be provided too.
        #  sampleRate: 48000
        #  channelCount: 2
        #  in case of G711, muLaw must be provided too.
        #  muLaw: false
```

By default, the server uses a default offline segment with the text "STREAM IS OFFLINE". The segment can be replaced with an external MP4 file:

```yml
paths:
  mypath:
    alwaysAvailable: true
    alwaysAvailableFile: "./h264.mp4"
```

By default, the stream is recorded even when no source is connected and the offline segment is playing. To record only when a source is connected, set `alwaysAvailableRecorded` to `false`:

```yml
paths:
  mypath:
    alwaysAvailable: true
    alwaysAvailableRecorded: false
    record: true
    alwaysAvailableTracks:
      - codec: H264
```

The option can also be used with a regular expression-based path (or with `all_others`). In that case paths are not created at startup: a path instance, together with its offline stream, is created on demand when a reader or publisher requests a matching name. The instance keeps serving readers while a publisher connects and disconnects, and is destroyed together with its offline resources after staying idle (no source and no readers) for a grace period. A request on a name that does not match any regular expression does not create any instance.
