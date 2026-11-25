package storage

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	covZBucket = "bucket-de-teste"
	covZAccess = "chave-de-teste"
	covZSecret = "segredo-de-teste-com-mais-de-32-bytes-ok"
)

// covZClient aponta o Client para o S3 falso. As variaveis AWS_* sao fixadas
// para o SDK nao tentar perfil, IMDS nem rede externa.
func covZClient(t *testing.T, f *covZS3, publicEndpoint string) *Client {
	t.Helper()
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	c, err := New(context.Background(), f.URL(), publicEndpoint, covZBucket, "us-east-1", covZAccess, covZSecret)
	require.NoError(t, err)
	return c
}

func TestCovZ_Client_PutGetRoundTrip(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")
	ctx := context.Background()

	payload := []byte("conteudo-de-audio-de-teste")
	require.NoError(t, c.Put(ctx, "evidence/2026/clip.mp3", bytes.NewReader(payload), "audio/mpeg"))

	stored, ok := f.object("evidence/2026/clip.mp3")
	require.True(t, ok, "o objeto chegou ao bucket pela chave certa (path-style)")
	assert.Equal(t, payload, stored.body)
	assert.Equal(t, "audio/mpeg", stored.contentType)
	assert.Equal(t, covZBucket, c.Bucket())
	assert.Contains(t, f.authSeen, "AWS4-HMAC-SHA256", "requisicao assinada (SigV4)")
	assert.Contains(t, f.authSeen, covZAccess)

	body, ct, size, err := c.Get(ctx, "evidence/2026/clip.mp3")
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equal(t, "audio/mpeg", ct)
	assert.Equal(t, int64(len(payload)), size)
}

func TestCovZ_Client_GetMissingKey(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")

	body, ct, size, err := c.Get(context.Background(), "nao/existe.mp3")
	require.Error(t, err)
	assert.Nil(t, body)
	assert.Empty(t, ct)
	assert.Zero(t, size)
	assert.Contains(t, err.Error(), "storage: get nao/existe.mp3")
	assert.True(t, IsNotFound(err), "objeto ausente e reconhecido por IsNotFound")
}

func TestCovZ_Client_GetFromWrongBucketIsNotNotFound(t *testing.T) {
	f := covZNewS3(t, "outro-bucket") // o cliente pede covZBucket => NoSuchBucket
	c := covZClient(t, f, "")

	_, _, _, err := c.Get(context.Background(), "k")
	require.Error(t, err)
	assert.False(t, IsNotFound(err), "bucket errado e problema de configuracao, nao objeto ausente")
}

func TestCovZ_Client_Head(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")
	ctx := context.Background()

	require.NoError(t, c.Put(ctx, "a/b.txt", strings.NewReader("12345"), "text/plain"))

	size, ct, err := c.Head(ctx, "a/b.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(5), size)
	assert.Equal(t, "text/plain", ct)

	_, _, err = c.Head(ctx, "a/ausente.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: head a/ausente.txt")
}

func TestCovZ_Client_DeleteIsIdempotent(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")
	ctx := context.Background()

	require.NoError(t, c.Put(ctx, "x.bin", strings.NewReader("abc"), "application/octet-stream"))
	_, ok := f.object("x.bin")
	require.True(t, ok)

	require.NoError(t, c.Delete(ctx, "x.bin"))
	_, ok = f.object("x.bin")
	assert.False(t, ok, "objeto removido")

	require.NoError(t, c.Delete(ctx, "x.bin"), "apagar de novo nao e erro")
	require.NoError(t, c.Delete(ctx, "nunca-existiu"))
}

func TestCovZ_Client_PutWithStorageClass(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")
	ctx := context.Background()

	require.NoError(t, c.PutWithStorageClass(ctx, "cold/clip.mp3", strings.NewReader("frio"), "audio/mpeg", "STANDARD_IA"))
	o, ok := f.object("cold/clip.mp3")
	require.True(t, ok)
	assert.Equal(t, "STANDARD_IA", o.storageClass)
	assert.Equal(t, "audio/mpeg", o.contentType)
	assert.Equal(t, []byte("frio"), o.body)

	// Classe vazia: nenhum header de storage class e enviado.
	require.NoError(t, c.PutWithStorageClass(ctx, "hot/clip.mp3", strings.NewReader("quente"), "audio/mpeg", ""))
	o, ok = f.object("hot/clip.mp3")
	require.True(t, ok)
	assert.Empty(t, o.storageClass)
}

func TestCovZ_Client_ErrorsAreWrappedWithKey(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	f.deny = true // 403 AccessDenied: o SDK nao tenta de novo
	c := covZClient(t, f, "")
	ctx := context.Background()

	err := c.Put(ctx, "k1", strings.NewReader("x"), "text/plain")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: put k1")

	err = c.PutWithStorageClass(ctx, "k2", strings.NewReader("x"), "text/plain", "STANDARD")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: put k2")

	err = c.Delete(ctx, "k3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: delete k3")

	_, _, err = c.Head(ctx, "k4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: head k4")

	_, _, _, err = c.Get(ctx, "k5")
	require.Error(t, err)
	assert.False(t, IsNotFound(err), "AccessDenied nao e 'nao encontrado'")
}

func TestCovZ_Client_CancelledContext(t *testing.T) {
	f := covZNewS3(t, covZBucket)
	c := covZClient(t, f, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, c.Put(ctx, "k", strings.NewReader("x"), "text/plain"))
	_, _, _, err := c.Get(ctx, "k")
	require.Error(t, err)
	assert.Empty(t, f.calls(), "contexto cancelado nao chega ao servidor")
}

func TestCovZ_Client_PresignGet(t *testing.T) {
	f := covZNewS3(t, covZBucket)

	t.Run("sem endpoint publico usa o endpoint do servidor", func(t *testing.T) {
		c := covZClient(t, f, "")
		before := time.Now()
		raw, expiresAt, err := c.PresignGet(context.Background(), "evidence/clip 1.mp3", 15*time.Minute)
		require.NoError(t, err)

		u, err := url.Parse(raw)
		require.NoError(t, err)
		assert.Equal(t, f.srv.Listener.Addr().String(), u.Host)
		assert.Equal(t, "/"+covZBucket+"/evidence/clip%201.mp3", u.EscapedPath(), "path-style com a chave escapada")
		q := u.Query()
		assert.Equal(t, "900", q.Get("X-Amz-Expires"))
		assert.NotEmpty(t, q.Get("X-Amz-Signature"))
		assert.True(t, strings.HasPrefix(q.Get("X-Amz-Credential"), covZAccess+"/"), "credencial leva a chave de acesso")
		assert.Equal(t, "AWS4-HMAC-SHA256", q.Get("X-Amz-Algorithm"))
		assert.WithinDuration(t, before.Add(15*time.Minute), expiresAt, 5*time.Second)
		assert.Empty(t, f.calls(), "presign e calculo local: nao chama o S3")
	})

	t.Run("com endpoint publico a URL aponta para ele", func(t *testing.T) {
		c := covZClient(t, f, "https://cdn.example.com")
		raw, _, err := c.PresignGet(context.Background(), "k.mp3", time.Minute)
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		assert.Equal(t, "https", u.Scheme)
		assert.Equal(t, "cdn.example.com", u.Host)
		assert.Equal(t, "60", u.Query().Get("X-Amz-Expires"))
	})

	t.Run("assinaturas diferem por chave", func(t *testing.T) {
		c := covZClient(t, f, "")
		a, _, err := c.PresignGet(context.Background(), "a.mp3", time.Minute)
		require.NoError(t, err)
		b, _, err := c.PresignGet(context.Background(), "b.mp3", time.Minute)
		require.NoError(t, err)
		ua, _ := url.Parse(a)
		ub, _ := url.Parse(b)
		assert.NotEqual(t, ua.Query().Get("X-Amz-Signature"), ub.Query().Get("X-Amz-Signature"))
	})
}
