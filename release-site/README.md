# Release Site

Static release page for `tacli`, intended for deployment on Vercel.

## Runtime config

The page reads these environment variables from `/api/config`:

- `RELEASE_REPO_OWNER`
- `RELEASE_REPO_NAME`
- `OSS_MIRROR_BASE`

Example OSS base:

```text
https://your-bucket.your-oss-endpoint/tacli
```

The client then resolves:

- GitHub latest asset: `https://github.com/<owner>/<repo>/releases/latest/download/<asset>`
- OSS latest asset: `<OSS_MIRROR_BASE>/latest/<asset>`
