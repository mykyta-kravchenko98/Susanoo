package helper

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// MustEnv reads a required environment variable, panicking with a clear
// message if it's unset. Used at Lambda cold-start (buildApp) in every
// cmd/* entrypoint - failing fast and loud here is preferable to a nil/empty
// config value causing a confusing error deep inside a handler later.
func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("%s env var is not set", key))
	}
	return v
}

// FetchSecret retrieves a plain-string secret (not SecretBinary) from
// Secrets Manager. Shared by every Lambda that needs the Telegram bot token
// (and, for the processor, the Anthropic API key).
func FetchSecret(ctx context.Context, client *secretsmanager.Client, secretName string) (string, error) {
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return "", fmt.Errorf("get secret %s: %w", secretName, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secret %s has no string value", secretName)
	}
	return *out.SecretString, nil
}
