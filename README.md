[![Go Reference](https://pkg.go.dev/badge/github.com/bitfield/weaver.svg)](https://pkg.go.dev/github.com/bitfield/weaver)
[![Go Report Card](https://goreportcard.com/badge/github.com/bitfield/weaver)](https://goreportcard.com/report/github.com/bitfield/weaver)

# Weaver

![Weaver logo](weaver.png)

`weaver` is a command-line tool for checking links on websites.

> *Old stories would tell how Weavers would kill each other over aesthetic disagreements, such as whether it was prettier to destroy an army of a thousand men or to leave it be, or whether a particular dandelion should or should not be plucked. For a Weaver, to think was to think aesthetically. To act—to Weave—was to bring about more pleasing patterns. They did not eat physical food: they seemed to subsist on the appreciation of beauty.*\
—China Miéville, [“Perdido Street Station”](https://amzn.to/4603LLS)


Here's how to install it:

```sh
go install github.com/bitfield/weaver/cmd/weaver@latest
```

To run it:

```sh
weaver https://example.com
```
```
Links: 2 (2 OK, 0 broken, 0 warnings) [1s]
```

## Verbose mode

To see more information about what's going on, use the `-v` flag:

```sh
weaver -v https://example.com
```
```
[OKAY] (200 OK) https://example.com/ (referrer: START)
[OKAY] (200 OK) https://www.iana.org/domains/example (referrer: https://example.com/)

Links: 2 (2 OK, 0 broken, 0 warnings) [900ms]
```

## How it works

The program checks the status of the specified URL. If the server responds with an HTML page, the program will parse this page for links, and check each new link for its status.

If the link points to the same domain as the original URL, it is also parsed for further links, and so on recursively until all links on the site have been visited.

Any broken links will be reported, together with the referring page:

```
[DEAD] (404 Not Found) https://example.com/bogus (referrer: https://example.com/)
```

## Rate limiting

The program attempts to continuously adapt its request rate to suit the server. On receiving a `429 Too Many Requests` response, it will reduce the current request rate. After a while with no further 429 responses, it will steadily increase the rate until it trips the rate limit once again.

Even without receiving any 429 responses, the program limits itself to a maximum of 5 requests per second, to be respectful of server resources.
