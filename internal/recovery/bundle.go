package recovery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/crypto/argon2"
	"gopkg.in/yaml.v3"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

const bundleSchema = 1

type manifest struct {
	Schema      int               `json:"schema"`
	CreatedAt   string            `json:"created_at"`
	Target      string            `json:"target"`
	Fingerprint string            `json:"expected_ssh_fingerprint,omitempty"`
	Files       map[string]string `json:"sha256"`
}

type envelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type BundleOptions struct {
	Config         *config.Config
	KnownHostsPath string
	SSHKeyPath     string
	Password       string
	IncludeSSHKey  bool
}

type BundleInfo struct {
	Schema      int
	CreatedAt   string
	Target      string
	Fingerprint string
	Files       map[string][]byte
}

// Create writes a checksummed portable recovery bundle atomically with mode
// 0600. The SSH key is encrypted independently with Argon2id and AES-256-GCM.
func Create(path string, options BundleOptions) error {
	if options.Config == nil {
		return fmt.Errorf("recovery bundle config is nil")
	}
	files := map[string][]byte{}
	configBytes, err := yaml.Marshal(options.Config)
	if err != nil {
		return fmt.Errorf("marshalling recovery config: %w", err)
	}
	files["config.yaml"] = configBytes

	if options.KnownHostsPath != "" {
		knownHosts, readErr := os.ReadFile(options.KnownHostsPath)
		if readErr != nil {
			return fmt.Errorf("reading SSH trust store: %w", readErr)
		}
		files["ssh/known_hosts"] = knownHosts
	}

	if options.IncludeSSHKey {
		if options.Password == "" {
			return fmt.Errorf("controller recovery password is empty")
		}
		privateKey, readErr := os.ReadFile(options.SSHKeyPath)
		if readErr != nil {
			return fmt.Errorf("reading controller SSH key: %w", readErr)
		}
		encrypted, encryptErr := encrypt(privateKey, options.Password)
		if encryptErr != nil {
			return encryptErr
		}
		files["ssh/private-key.enc"] = encrypted
	}

	checksums := make(map[string]string, len(files))
	for name, content := range files {
		sum := sha256.Sum256(content)
		checksums[name] = fmt.Sprintf("%x", sum[:])
	}
	metadata := manifest{
		Schema:      bundleSchema,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Target:      options.Config.TargetHost,
		Fingerprint: options.Config.SSHHostFingerprint,
		Files:       checksums,
	}
	manifestBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	files["manifest.json"] = append(manifestBytes, '\n')

	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mode := int64(0600)
		if name == "config.yaml" || name == "manifest.json" {
			mode = 0644
		}
		header := &tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(files[name])),
			Typeflag: tar.TypeReg,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(files[name]); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	return atomicWrite(path, archive.Bytes())
}

func encrypt(plaintext []byte, password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, plaintext, []byte("bootstrap-home-infra/controller-key/v1"))
	payload := envelope{
		Version:    1,
		KDF:        "argon2id:m=65536,t=3,p=4",
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}
	output, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return append(output, '\n'), nil
}

// Inspect verifies every manifest checksum and returns the protected bundle
// contents for a read-only recovery audit.
func Inspect(path string) (BundleInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return BundleInfo{}, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return BundleInfo{}, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	files := map[string][]byte{}
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return BundleInfo{}, nextErr
		}
		if header.Typeflag != tar.TypeReg || filepath.IsAbs(header.Name) || filepath.Clean(header.Name) != header.Name {
			return BundleInfo{}, fmt.Errorf("unsafe recovery bundle entry %q", header.Name)
		}
		content, readErr := io.ReadAll(io.LimitReader(tarReader, 16*1024*1024))
		if readErr != nil {
			return BundleInfo{}, readErr
		}
		files[header.Name] = content
	}
	var metadata manifest
	if err := json.Unmarshal(files["manifest.json"], &metadata); err != nil {
		return BundleInfo{}, fmt.Errorf("decoding recovery manifest: %w", err)
	}
	if metadata.Schema != bundleSchema {
		return BundleInfo{}, fmt.Errorf("unsupported recovery bundle schema %d", metadata.Schema)
	}
	for name, expected := range metadata.Files {
		content, ok := files[name]
		if !ok {
			return BundleInfo{}, fmt.Errorf("recovery bundle is missing %q", name)
		}
		sum := sha256.Sum256(content)
		if fmt.Sprintf("%x", sum[:]) != expected {
			return BundleInfo{}, fmt.Errorf("recovery bundle checksum mismatch for %q", name)
		}
	}
	delete(files, "manifest.json")
	return BundleInfo{
		Schema:      metadata.Schema,
		CreatedAt:   metadata.CreatedAt,
		Target:      metadata.Target,
		Fingerprint: metadata.Fingerprint,
		Files:       files,
	}, nil
}

// DecryptSSHKey opens the independently encrypted SSH-key envelope.
func DecryptSSHKey(encrypted []byte, password string) ([]byte, error) {
	var payload envelope
	if err := json.Unmarshal(encrypted, &payload); err != nil {
		return nil, fmt.Errorf("decoding SSH-key envelope: %w", err)
	}
	if payload.Version != 1 {
		return nil, fmt.Errorf("unsupported SSH-key envelope version %d", payload.Version)
	}
	salt, err := base64.StdEncoding.DecodeString(payload.Salt)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(payload.Nonce)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("invalid SSH-key envelope nonce")
	}
	plaintext, err := aead.Open(nil, nonce, sealed, []byte("bootstrap-home-infra/controller-key/v1"))
	if err != nil {
		return nil, fmt.Errorf("decrypting SSH-key envelope: %w", err)
	}
	return plaintext, nil
}

// Restore verifies a bundle and stages its contents in a new protected
// directory. It never changes the operator's live SSH or bootstrap paths.
func Restore(bundlePath, outputDir, password string) error {
	info, err := Inspect(bundlePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		return fmt.Errorf("recovery output directory %q must not already exist", outputDir)
	}
	var privateKey []byte
	if encryptedKey, ok := info.Files["ssh/private-key.enc"]; ok {
		privateKey, err = DecryptSSHKey(encryptedKey, password)
		if err != nil {
			return err
		}
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".controller-recovery-*.tmp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(staging, "ssh"), 0700); err != nil {
		return err
	}
	write := func(relative string, content []byte, mode os.FileMode) error {
		path := filepath.Join(staging, relative)
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	}
	if configBytes, ok := info.Files["config.yaml"]; ok {
		if err := write("config.yaml", configBytes, 0600); err != nil {
			return err
		}
	}
	if knownHosts, ok := info.Files["ssh/known_hosts"]; ok {
		if err := write("ssh/known_hosts", knownHosts, 0600); err != nil {
			return err
		}
	}
	if privateKey != nil {
		if err := write("ssh/id_ed25519", privateKey, 0600); err != nil {
			return err
		}
	}
	return os.Rename(staging, outputDir)
}

func atomicWrite(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "controller-bundle-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
