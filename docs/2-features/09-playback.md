# Playback recorded streams

Existing recordings can be played back to users through a dedicated HTTP server, that can be enabled inside the configuration:

```yml
playback: yes
playbackAddress: :9996
```

The server provides an endpoint to list recorded timespans:

```
http://localhost:9996/list?path=[mypath]&start=[start]&end=[end]
```

Where:

- [mypath] is the name of a path
- [start] (optional) is the start date in [RFC3339 format](https://www.utctime.net/)
- [end] (optional) is the end date in [RFC3339 format](https://www.utctime.net/)

The server will return a list of timespans in JSON format:

```json
[
  {
    "start": "2006-01-02T15:04:05Z07:00",
    "duration": 60.0,
    "url": "http://localhost:9996/get?path=[mypath]&start=2006-01-02T15%3A04%3A05Z07%3A00&duration=60.0"
  },
  {
    "start": "2006-01-02T15:07:05Z07:00",
    "duration": 32.33,
    "url": "http://localhost:9996/get?path=[mypath]&start=2006-01-02T15%3A07%3A05Z07%3A00&duration=32.33"
  }
]
```

The server provides an endpoint that reports which parts of a time range are
actually covered by recordings, and which parts are gaps:

```
http://localhost:9996/coverage?path=[mypath]&start=[start]&end=[end]
```

Where:

- [mypath] is the name of a path
- [start] is the start date in [RFC3339 format](https://www.utctime.net/)
- [end] is the end date in [RFC3339 format](https://www.utctime.net/) and must be after [start]

The endpoint reads the duration stored in the header of every segment, without
reading its media payload, so the end of a recording can extend past the start
of the next one, cross midnight and cross day-based folders of the recording
path template. Consecutive and overlapping segments are merged into a single
interval, and each interval lists the segments it is made of.

```json
{
  "start": "2024-03-10T23:50:00Z07:00",
  "end": "2024-03-11T00:20:00Z07:00",
  "intervals": [
    {
      "start": "2024-03-10T23:55:00Z07:00",
      "end": "2024-03-11T00:05:00Z07:00",
      "segments": [
        {
          "path": "/recordings/mypath/2024-03-10/2024-03-10_23-55-00-000000.mp4",
          "start": "2024-03-10T23:55:00Z07:00",
          "end": "2024-03-11T00:05:00Z07:00"
        }
      ]
    }
  ],
  "gaps": [
    {
      "start": "2024-03-10T23:50:00Z07:00",
      "end": "2024-03-10T23:55:00Z07:00"
    },
    {
      "start": "2024-03-11T00:05:00Z07:00",
      "end": "2024-03-11T00:20:00Z07:00"
    }
  ],
  "skipped": []
}
```

Segments that disappeared while the query was running, whose header is damaged,
or whose duration is zero, do not contribute to coverage: they are listed in
`skipped` together with the reason, and their time span appears as a gap.

The endpoint supports the fMP4 record format, since it is the only one that
stores a duration inside segment headers. Requests that exceed the maximum
number of segments (100000) are rejected with status `413`.

The server provides an endpoint to download recordings:

```
http://localhost:9996/get?path=[mypath]&start=[start]&duration=[duration]&format=[format]
```

Where:

- [mypath] is the path name
- [start] is the start date in [RFC3339 format](https://www.utctime.net/)
- [duration] is the maximum duration of the recording in seconds
- [format] (optional) is the output format of the stream. Available values are "fmp4" (default) and "mp4"

All parameters must be [url-encoded](https://www.urlencoder.org/). For instance:

```
http://localhost:9996/get?path=mypath&start=2024-01-14T16%3A33%3A17%2B00%3A00&duration=200.5
```

The resulting stream uses the fMP4 format, that is natively compatible with any browser, therefore its URL can be directly inserted into a \<video> tag:

```html
<video controls>
  <source
    src="http://localhost:9996/get?path=[mypath]&start=[start_date]&duration=[duration]"
    type="video/mp4"
  />
</video>
```

The fMP4 format may offer limited compatibility with some players. To fix the issue, it's possible to use the standard MP4 format, by adding `format=mp4` to a `/get` request:

```
http://localhost:9996/get?path=[mypath]&start=[start_date]&duration=[duration]&format=mp4
```
