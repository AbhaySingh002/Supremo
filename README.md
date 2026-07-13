<div align="center">
  <img src="https://icons.veryicon.com/png/o/movie--tv/movie-hero-icon/superman-4.png" width="300" height="300" alt="Superman Logo" style="background-color: black; border-radius: 8px; padding: 5px;" />
</div>

## Usage

```sh
git clone <repo-url>
cd supremo
make build   # build binary to ./supremo
make run     # or run directly
make test    # run tests
make clean   # remove binary
```

Config and credentials are auto-created on first run in `.supremo/`. Defaults to Gemini.

Set your API key and model from the CLI:
```
> /auth <your-gemini-api-key>
> /model gemini-2.5-flash
```

View or reload config:
```
> /config
> /config reload
```

Note: Only Gemini is supported currently. More providers coming soon.
