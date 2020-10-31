**BigFiles** is a (partial) Go implementation of a [Git-LFS
v2.12.0](https://github.com/git-lfs/git-lfs/tree/v2.12.0/docs/api) server.

- It can be configured to use any S3-API-compatible backend for LFS storage.
- It does not currently implent the locking API.
- See [the example](example/main.go).
