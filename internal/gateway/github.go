package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const githubAPIVersion = "2022-11-28"

type GitHubService struct {
	store        *GatewayStore
	box          cipher.AEAD
	httpClient   *http.Client
	clientID     string
	clientSecret string
	redirectURL  string
	scopes       string
	apiBaseURL   string
	webBaseURL   string
	gitTimeout   time.Duration
	gitMu        sync.Mutex
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type GitHubRepo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	UpdatedAt     string `json:"updated_at"`
	CloneURL      string `json:"-"`
}

type githubAPIRepo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	UpdatedAt     string `json:"updated_at"`
	CloneURL      string `json:"clone_url"`
}

func (r githubAPIRepo) public() GitHubRepo {
	return GitHubRepo{ID: r.ID, FullName: r.FullName, Name: r.Name, Owner: r.Owner.Login,
		Private: r.Private, Description: r.Description, DefaultBranch: r.DefaultBranch,
		HTMLURL: r.HTMLURL, UpdatedAt: r.UpdatedAt, CloneURL: r.CloneURL}
}

func NewGitHubService(opts Options, store *GatewayStore, stateRoot string) (*GitHubService, error) {
	box, err := newGitHubCredentialBox(stateRoot, opts.GitHubTokenKey)
	if err != nil {
		return nil, err
	}
	return &GitHubService{
		store: store, box: box, httpClient: &http.Client{Timeout: 30 * time.Second},
		clientID: strings.TrimSpace(opts.GitHubClientID), clientSecret: strings.TrimSpace(opts.GitHubClientSecret),
		redirectURL: strings.TrimSpace(opts.GitHubRedirectURL), scopes: strings.TrimSpace(opts.GitHubScopes),
		apiBaseURL: strings.TrimRight(opts.GitHubAPIBaseURL, "/"), webBaseURL: strings.TrimRight(opts.GitHubWebBaseURL, "/"),
		gitTimeout: opts.GitHubGitTimeout,
	}, nil
}

func newGitHubCredentialBox(stateRoot, configuredKey string) (cipher.AEAD, error) {
	var key []byte
	if configuredKey != "" {
		sum := sha256.Sum256([]byte(configuredKey))
		key = sum[:]
	} else {
		if err := os.MkdirAll(stateRoot, 0o700); err != nil {
			return nil, err
		}
		keyPath := filepath.Join(stateRoot, "github-token.key")
		stored, err := os.ReadFile(keyPath)
		if err == nil {
			key = stored
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		} else {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					key, err = os.ReadFile(keyPath)
				}
				if err != nil {
					return nil, err
				}
			} else {
				if _, err = file.Write(key); err != nil {
					_ = file.Close()
					return nil, err
				}
				if err = file.Close(); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("github credential key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (g *GitHubService) oauthConfigured() bool {
	return g != nil && g.clientID != "" && g.clientSecret != ""
}

func (g *GitHubService) encryptToken(accountID int, token string) (string, error) {
	nonce := make([]byte, g.box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := g.box.Seal(nonce, nonce, []byte(token), []byte(strconv.Itoa(accountID)))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (g *GitHubService) decryptToken(credential GitHubCredential) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(credential.TokenCipher)
	if err != nil || len(raw) < g.box.NonceSize() {
		return "", errors.New("invalid github credential")
	}
	nonce := raw[:g.box.NonceSize()]
	plain, err := g.box.Open(nil, nonce, raw[g.box.NonceSize():], []byte(strconv.Itoa(credential.AccountID)))
	if err != nil {
		return "", errors.New("cannot decrypt github credential")
	}
	return string(plain), nil
}

func (g *GitHubService) saveCredential(ctx context.Context, accountID int, token, login, method string) error {
	ciphertext, err := g.encryptToken(accountID, token)
	if err != nil {
		return err
	}
	return g.store.UpsertGitHubCredential(ctx, GitHubCredential{AccountID: accountID,
		TokenCipher: ciphertext, Login: login, AuthMethod: method, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

func (g *GitHubService) credential(ctx context.Context, accountID int) (GitHubCredential, string, error) {
	credential, err := g.store.GitHubCredential(ctx, accountID)
	if err != nil {
		return GitHubCredential{}, "", err
	}
	token, err := g.decryptToken(credential)
	return credential, token, err
}

func (g *GitHubService) api(ctx context.Context, method, path, token string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, g.apiBaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiError struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiError)
		if apiError.Message == "" {
			apiError.Message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("github API %d: %s", resp.StatusCode, apiError.Message)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func (g *GitHubService) connectToken(ctx context.Context, accountID int, token string) (githubUser, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return githubUser{}, errors.New("token is required")
	}
	var user githubUser
	if err := g.api(ctx, http.MethodGet, "/user", token, nil, &user); err != nil {
		return githubUser{}, err
	}
	if user.Login == "" {
		return githubUser{}, errors.New("github user response has no login")
	}
	return user, g.saveCredential(ctx, accountID, token, user.Login, "pat")
}

func (g *GitHubService) authorizeURL(ctx context.Context, accountID int, returnPath string) (string, error) {
	if !g.oauthConfigured() {
		return "", errors.New("github oauth is not configured")
	}
	if !validGitHubReturnPath(returnPath) {
		returnPath = "/code"
	}
	state, err := randomToken("")
	if err != nil {
		return "", err
	}
	if err := g.store.CreateGitHubOAuthState(ctx, tokenDigest(state), accountID, returnPath, time.Now().Add(10*time.Minute)); err != nil {
		return "", err
	}
	values := url.Values{"client_id": {g.clientID}, "state": {state}, "scope": {g.scopes}}
	if g.redirectURL != "" {
		values.Set("redirect_uri", g.redirectURL)
	}
	return g.webBaseURL + "/login/oauth/authorize?" + values.Encode(), nil
}

func validGitHubReturnPath(path string) bool {
	return path == "/code" || path == "/settings"
}

func (g *GitHubService) exchangeOAuth(ctx context.Context, code, state string) (int, string, githubUser, error) {
	accountID, returnPath, err := g.store.ConsumeGitHubOAuthState(ctx, tokenDigest(state), time.Now().UTC())
	if err != nil {
		return 0, "/code", githubUser{}, err
	}
	values := url.Values{"client_id": {g.clientID}, "client_secret": {g.clientSecret}, "code": {code}, "state": {state}}
	if g.redirectURL != "" {
		values.Set("redirect_uri", g.redirectURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.webBaseURL+"/login/oauth/access_token", strings.NewReader(values.Encode()))
	if err != nil {
		return accountID, returnPath, githubUser{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return accountID, returnPath, githubUser{}, err
	}
	defer resp.Body.Close()
	var exchange struct {
		AccessToken      string `json:"access_token"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&exchange); err != nil {
		return accountID, returnPath, githubUser{}, err
	}
	if resp.StatusCode != http.StatusOK || exchange.AccessToken == "" {
		if exchange.ErrorDescription == "" {
			exchange.ErrorDescription = "github oauth exchange failed"
		}
		return accountID, returnPath, githubUser{}, errors.New(exchange.ErrorDescription)
	}
	var user githubUser
	if err := g.api(ctx, http.MethodGet, "/user", exchange.AccessToken, nil, &user); err != nil {
		return accountID, returnPath, githubUser{}, err
	}
	if err := g.saveCredential(ctx, accountID, exchange.AccessToken, user.Login, "oauth"); err != nil {
		return accountID, returnPath, githubUser{}, err
	}
	return accountID, returnPath, user, nil
}

func (g *GitHubService) repositories(ctx context.Context, accountID, perPage int) ([]GitHubRepo, error) {
	_, token, err := g.credential(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if perPage < 1 || perPage > 100 {
		perPage = 50
	}
	var raw []githubAPIRepo
	path := "/user/repos?affiliation=owner%2Ccollaborator%2Corganization_member&sort=updated&per_page=" + strconv.Itoa(perPage)
	if err := g.api(ctx, http.MethodGet, path, token, nil, &raw); err != nil {
		return nil, err
	}
	repos := make([]GitHubRepo, 0, len(raw))
	for _, repo := range raw {
		repos = append(repos, repo.public())
	}
	return repos, nil
}

func (g *GitHubService) repository(ctx context.Context, accountID int, owner, repo string) (GitHubRepo, string, error) {
	_, token, err := g.credential(ctx, accountID)
	if err != nil {
		return GitHubRepo{}, "", err
	}
	var raw githubAPIRepo
	endpoint := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	if err := g.api(ctx, http.MethodGet, endpoint, token, nil, &raw); err != nil {
		return GitHubRepo{}, "", err
	}
	return raw.public(), token, nil
}

func (g *GitHubService) connected(ctx context.Context, accountID int) (GitHubCredential, bool, error) {
	credential, err := g.store.GitHubCredential(ctx, accountID)
	if isMissingGitHubCredential(err) {
		return GitHubCredential{}, false, nil
	}
	if err != nil {
		return GitHubCredential{}, false, err
	}
	return credential, true, nil
}
