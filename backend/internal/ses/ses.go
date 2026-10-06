// Package ses sends emails through Amazon SES.
package ses

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
)

// Sender sends plain-text emails with the credentials of the given configuration.
type Sender struct{}

// Send sends a plain-text email from cfg.From to one recipient.
func (Sender) Send(ctx context.Context, cfg *settings.Email, to, subject, body string) error {
	client := sesv2.New(sesv2.Options{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	})
	utf8 := func(s string) *types.Content {
		return &types.Content{Data: aws.String(s), Charset: aws.String("UTF-8")}
	}
	_, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(cfg.From),
		Destination:      &types.Destination{ToAddresses: []string{to}},
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: utf8(subject),
			Body:    &types.Body{Text: utf8(body)},
		}},
	})
	if err != nil {
		return fmt.Errorf("ses: %w", err)
	}
	return nil
}
