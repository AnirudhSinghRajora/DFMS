package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/AnirudhSinghRajora/DFMS/internal/cliconfig"
	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
)

// apiSession bundles everything a command needs to talk to the active server:
// an API client wired for transparent auth, the token store for that context,
// the context's name (for messages), and the resolved upload threshold above
// which uploads switch to the chunked multipart protocol.
type apiSession struct {
	client             *dfmsclient.Client
	tokens             dfmsclient.TokenStore
	contextName        string
	multipartThreshold int64
}

// session resolves the active context and constructs an apiSession for it. It
// fails if no context is configured/active so commands give a clear next step.
func (e *appEnv) session() (*apiSession, error) {
	cfg, _, err := e.load()
	if err != nil {
		return nil, err
	}
	name := e.activeContextName(cfg)
	if name == "" {
		return nil, errors.New("no active context; add one with 'dfmsctl context add <name> --url <url>'")
	}
	ctx, ok := cfg.Context(name)
	if !ok {
		return nil, fmt.Errorf("active context %q is not configured", name)
	}

	secrets, err := cliconfig.NewSecretStore()
	if err != nil {
		return nil, fmt.Errorf("initializing secret store: %w", err)
	}
	store := contextTokens{secrets: secrets, context: name}

	httpClient := dfmsclient.NewAuthHTTPClient(ctx.URL, store, ctx.InsecureSkipVerify)
	client := dfmsclient.New(ctx.URL, dfmsclient.WithHTTPClient(httpClient))

	threshold := defaultMultipartThreshold
	if cfg.Defaults != nil && cfg.Defaults.MultipartThreshold > 0 {
		threshold = cfg.Defaults.MultipartThreshold
	}

	return &apiSession{
		client:             client,
		tokens:             store,
		contextName:        name,
		multipartThreshold: threshold,
	}, nil
}

// contextTokens adapts a context-keyed cliconfig.SecretStore to the
// single-context dfmsclient.TokenStore the client expects, serializing tokens as
// JSON. It keeps the client decoupled from the CLI's storage format.
type contextTokens struct {
	secrets cliconfig.SecretStore
	context string
}

func (a contextTokens) Load() (dfmsclient.Tokens, error) {
	data, err := a.secrets.Get(a.context)
	if errors.Is(err, cliconfig.ErrNotFound) {
		return dfmsclient.Tokens{}, dfmsclient.ErrNoCredentials
	}
	if err != nil {
		return dfmsclient.Tokens{}, err
	}
	var tokens dfmsclient.Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return dfmsclient.Tokens{}, fmt.Errorf("decoding stored tokens: %w", err)
	}
	return tokens, nil
}

func (a contextTokens) Save(tokens dfmsclient.Tokens) error {
	data, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("encoding tokens: %w", err)
	}
	return a.secrets.Set(a.context, data)
}

func (a contextTokens) Delete() error {
	if err := a.secrets.Delete(a.context); err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return err
	}
	return nil
}
