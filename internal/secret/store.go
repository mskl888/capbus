// Package secret stores credentials that capabilities need, so they never
// appear in an agent's config file.
//
// Threat model, stated plainly: the master key lives on the same machine, so a
// process running as your user can read it. This protects against credential
// sprawl - the same token copied into four agent configs, committed by accident,
// pasted into a chat, or slurped into an LLM context. It does not protect
// against malware running as you, and it is not a vault.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	fileVersion = 1
	keyLen      = 32
)

type disk struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

type item struct {
	Value   string    `json:"value"`
	Updated time.Time `json:"updated"`
}

type payload struct {
	Items map[string]item `json:"items"`
}

// Store is the on-disk secret store for this user.
type Store struct {
	dir string
	key []byte
}

// Dir is ~/.capbus, or $CAPBUS_HOME.
func Dir() string {
	if d := os.Getenv("CAPBUS_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".capbus"
	}
	return filepath.Join(home, ".capbus")
}

func storePath(dir string) string { return filepath.Join(dir, "secrets.json") }
func keyPath(dir string) string   { return filepath.Join(dir, "master.key") }

// Open loads the store for the current user, creating the key on first use.
func Open() (*Store, error) { return OpenAt(Dir()) }

// OpenAt is Open with an explicit directory, which tests rely on.
func OpenAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory's mode alone, so a store created
	// by an earlier 0775 parent stays group-writable. The master key lives
	// here; make the mode authoritative on every open.
	if err := os.Chmod(dir, 0o700); err != nil && !os.IsPermission(err) {
		return nil, err
	}
	key, err := loadKey(dir)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, key: key}, nil
}

// loadKey prefers $CAPBUS_MASTER_KEY, then the key file, then generates one.
// An env var is the better place for it: put it in your shell profile or an OS
// keychain rather than next to the encrypted file.
func loadKey(dir string) ([]byte, error) {
	if h := strings.TrimSpace(os.Getenv("CAPBUS_MASTER_KEY")); h != "" {
		k, err := hex.DecodeString(h)
		if err != nil || len(k) != keyLen {
			return nil, fmt.Errorf("CAPBUS_MASTER_KEY must be %d hex characters", keyLen*2)
		}
		return k, nil
	}

	if b, err := os.ReadFile(keyPath(dir)); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != keyLen {
			return nil, fmt.Errorf("%s is not a valid master key", keyPath(dir))
		}
		return k, nil
	}
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	// Create exclusively: two capbus processes starting at once must not each
	// generate a key and have one of them lose, orphaning the other's secrets.
	f, err := os.OpenFile(keyPath(dir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return readKey(keyPath(dir))
		}
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(k)); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return k, nil
}

func readKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != keyLen {
		return nil, fmt.Errorf("%s is not a valid master key", path)
	}
	return k, nil
}

func (s *Store) seal(plain []byte) (nonce, data []byte, err error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, g.Seal(nil, nonce, plain, nil), nil
}

func (s *Store) open(nonce, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, nonce, data, nil)
}

func (s *Store) load() (*payload, error) {
	p := &payload{Items: map[string]item{}}
	b, err := os.ReadFile(storePath(s.dir))
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, err
	}
	var d disk
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", storePath(s.dir), err)
	}
	nonce, err := hex.DecodeString(d.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%s: bad nonce", storePath(s.dir))
	}
	raw, err := hex.DecodeString(d.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: bad payload", storePath(s.dir))
	}
	plain, err := s.open(nonce, raw)
	if err != nil {
		// Wrong master key is the usual cause, and it is worth saying so.
		return nil, errors.New("cannot decrypt secrets: wrong master key?")
	}
	if err := json.Unmarshal(plain, p); err != nil {
		return nil, err
	}
	if p.Items == nil {
		p.Items = map[string]item{}
	}
	return p, nil
}

func (s *Store) save(p *payload) error {
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}
	nonce, data, err := s.seal(plain)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(disk{
		Version: fileVersion,
		Nonce:   hex.EncodeToString(nonce),
		Data:    hex.EncodeToString(data),
	}, "", "  ")
	if err != nil {
		return err
	}
	path := storePath(s.dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Set stores a value, replacing any previous one.
func (s *Store) Set(name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("secret name is required")
	}
	if value == "" {
		return errors.New("secret value is empty")
	}
	p, err := s.load()
	if err != nil {
		return err
	}
	p.Items[name] = item{Value: value, Updated: time.Now()}
	return s.save(p)
}

// Get returns a secret value.
func (s *Store) Get(name string) (string, bool, error) {
	p, err := s.load()
	if err != nil {
		return "", false, err
	}
	it, ok := p.Items[name]
	return it.Value, ok, nil
}

// Delete removes a secret.
func (s *Store) Delete(name string) error {
	p, err := s.load()
	if err != nil {
		return err
	}
	delete(p.Items, name)
	return s.save(p)
}

// Info is the safe-to-print view of a stored secret: never the value.
type Info struct {
	Name    string
	Updated time.Time
}

// List returns every stored secret's metadata, sorted by name.
func (s *Store) List() ([]Info, error) {
	p, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(p.Items))
	for n, it := range p.Items {
		out = append(out, Info{Name: n, Updated: it.Updated})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve returns the values for the named secrets and the names that are
// missing. Missing names are not an error here: doctor wants to report them.
func (s *Store) Resolve(names []string) (map[string]string, []string, error) {
	p, err := s.load()
	if err != nil {
		return nil, nil, err
	}
	vals := map[string]string{}
	var missing []string
	for _, n := range names {
		it, ok := p.Items[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		vals[n] = it.Value
	}
	sort.Strings(missing)
	return vals, missing, nil
}

// EnvList renders resolved secrets as KEY=value pairs for a child process.
func EnvList(vals map[string]string) []string {
	out := make([]string, 0, len(vals))
	for k, v := range vals {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
