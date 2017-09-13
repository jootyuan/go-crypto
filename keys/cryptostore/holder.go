package cryptostore

import (
	"strings"

	crypto "github.com/tendermint/go-crypto"
	keys "github.com/tendermint/go-crypto/keys"
)

// Manager combines encyption and storage implementation to provide
// a full-featured key manager
type Manager struct {
	es    encryptedStorage
	codec keys.Codec
}

func New(coder Encoder, store keys.Storage, codec keys.Codec) Manager {
	return Manager{
		es: encryptedStorage{
			coder: coder,
			store: store,
		},
		codec: codec,
	}
}

// assert Manager satisfies keys.Signer and keys.Manager interfaces
var _ keys.Signer = Manager{}
var _ keys.Manager = Manager{}

// Create adds a new key to the storage engine, returning error if
// another key already stored under this name
//
// algo must be a supported go-crypto algorithm: ed25519, secp256k1
func (s Manager) Create(name, passphrase, algo string) (keys.Info, string, error) {
	// 128-bits are the all the randomness we can make use of
	secret := crypto.CRandBytes(16)
	gen := getGenerator(algo)

	key, err := gen.Generate(secret)
	if err != nil {
		return keys.Info{}, "", err
	}

	err = s.es.Put(name, passphrase, key)
	if err != nil {
		return keys.Info{}, "", err
	}

	// we append the type byte to the serialized secret to help with recovery
	// ie [secret] = [secret] + [type]
	typ := key.Bytes()[0]
	secret = append(secret, typ)

	seed, err := s.codec.BytesToWords(secret)
	phrase := strings.Join(seed, " ")
	return info(name, key), phrase, err
}

// Recover takes a seed phrase and tries to recover the private key.
//
// If the seed phrase is valid, it will create the private key and store
// it under name, protected by passphrase.
//
// Result similar to New(), except it doesn't return the seed again...
func (s Manager) Recover(name, passphrase, seedphrase string) (keys.Info, error) {
	words := strings.Split(strings.TrimSpace(seedphrase), " ")
	secret, err := s.codec.WordsToBytes(words)
	if err != nil {
		return keys.Info{}, err
	}

	// secret is comprised of the actual secret with the type appended
	// ie [secret] = [secret] + [type]
	l := len(secret)
	secret, typ := secret[:l-1], secret[l-1]

	gen := getGeneratorByType(typ)
	key, err := gen.Generate(secret)
	if err != nil {
		return keys.Info{}, err
	}

	// d00d, it worked!  create the bugger....
	err = s.es.Put(name, passphrase, key)
	return info(name, key), err
}

// List loads the keys from the storage and enforces alphabetical order
func (s Manager) List() (keys.Infos, error) {
	res, err := s.es.List()
	res.Sort()
	return res, err
}

// Get returns the public information about one key
func (s Manager) Get(name string) (keys.Info, error) {
	_, _, info, err := s.es.store.Get(name)
	return info, err
}

// Sign will modify the Signable in order to attach a valid signature with
// this public key
//
// If no key for this name, or the passphrase doesn't match, returns an error
func (s Manager) Sign(name, passphrase string, tx keys.Signable) error {
	key, _, err := s.es.Get(name, passphrase)
	if err != nil {
		return err
	}
	sig := key.Sign(tx.SignBytes())
	pubkey := key.PubKey()
	return tx.Sign(pubkey, sig)
}

// Export decodes the private key with the current password, encodes
// it with a secure one-time password and generates a sequence that can be
// Imported by another Manager
//
// This is designed to copy from one device to another, or provide backups
// during version updates.
// TODO: How to handle Export with salt?
func (s Manager) Export(name, oldpass, transferpass string) ([]byte, []byte, error) {
	key, _, err := s.es.Get(name, oldpass)
	if err != nil {
		return nil, nil, err
	}

	salt, res, err := s.es.coder.Encrypt(key, transferpass)
	return salt, res, err
}

// Import accepts bytes generated by Export along with the same transferpass
// If they are valid, it stores the key under the given name with the
// new passphrase.
// TODO: How to handle Import with salt?
func (s Manager) Import(name, newpass, transferpass string, salt, data []byte) error {
	key, err := s.es.coder.Decrypt(salt, data, transferpass)
	if err != nil {
		return err
	}

	return s.es.Put(name, newpass, key)
}

// Delete removes key forever, but we must present the
// proper passphrase before deleting it (for security)
func (s Manager) Delete(name, passphrase string) error {
	// verify we have the proper password before deleting
	_, _, err := s.es.Get(name, passphrase)
	if err != nil {
		return err
	}
	return s.es.Delete(name)
}

// Update changes the passphrase with which a already stored key is encoded.
//
// oldpass must be the current passphrase used for encoding, newpass will be
// the only valid passphrase from this time forward
func (s Manager) Update(name, oldpass, newpass string) error {
	key, _, err := s.es.Get(name, oldpass)
	if err != nil {
		return err
	}

	// we must delete first, as Putting over an existing name returns an error
	s.Delete(name, oldpass)

	return s.es.Put(name, newpass, key)
}
