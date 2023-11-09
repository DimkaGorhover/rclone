package s3

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/endpointcreds"
	"github.com/aws/aws-sdk-go/aws/request"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	httpProviderEnvVar           = `AWS_CONTAINER_CREDENTIALS_FULL_URI`
	providerName                 = `AWSSigningHelperCredentialsEndpointProvider`
	sessionTokenTTLSecondsHeader = `x-aws-ec2-metadata-token-ttl-seconds`
	tokenResourcePath            = `/latest/api/token`
	sessionTokenMaxTTL           = 6 * time.Hour
)

func newAWSSigningHelperRemoteCredProvider(cfg *aws.Config, handlers request.Handlers) credentials.Provider {
	if u := os.Getenv(httpProviderEnvVar); len(u) > 0 {
		return localHTTPCredProvider(cfg, handlers, u)
	}
	return credentials.ErrorProvider{
		Err:          fmt.Errorf(`env var "%s" is not provided`, httpProviderEnvVar),
		ProviderName: providerName,
	}
}

func localHTTPCredProvider(cfg *aws.Config, handlers request.Handlers, u string) credentials.Provider {
	var errMsg string

	parsed, err := url.Parse(u)
	if err != nil {
		errMsg = fmt.Sprintf("invalid URL, %v", err)
	} else {
		host := aws.URLHostname(parsed)
		if len(host) == 0 {
			errMsg = "unable to parse host from local HTTP cred provider URL"
		}
	}

	if len(errMsg) > 0 {
		if cfg.Logger != nil {
			cfg.Logger.Log("Ignoring, HTTP credential provider", errMsg, err)
		}
		return credentials.ErrorProvider{
			Err:          awserr.New("CredentialsEndpointError", errMsg, err),
			ProviderName: providerName,
		}
	}

	return httpCredProvider(cfg, handlers, u)
}

func httpCredProvider(cfg *aws.Config, handlers request.Handlers, u string) credentials.Provider {
	p := endpointcreds.NewProviderClient(*cfg, handlers, u)
	endpointCredsProvider, ok := p.(*endpointcreds.Provider)
	if !ok {
		return credentials.ErrorProvider{
			Err:          errors.New(`returned provider is not endpoint credentials provider`),
			ProviderName: providerName,
		}
	}
	endpointCredsProvider.ExpiryWindow = 5 * time.Minute
	return &awsSigningHelperProvider{
		cfg:              cfg,
		ttl:              sessionTokenMaxTTL,
		httpClient:       http.DefaultClient,
		awsCredsProvider: endpointCredsProvider,
	}
}

type awsSigningHelperProvider struct {
	cfg              *aws.Config
	ttl              time.Duration
	httpClient       *http.Client
	awsCredsProvider *endpointcreds.Provider
}

func (p *awsSigningHelperProvider) IsExpired() bool {
	return p.awsCredsProvider.IsExpired()
}

func (p *awsSigningHelperProvider) Retrieve() (credentials.Value, error) {
	return p.RetrieveWithContext(aws.BackgroundContext())
}

func (p *awsSigningHelperProvider) RetrieveWithContext(ctx credentials.Context) (credentials.Value, error) {
	token, err := p.receiveToken(ctx)
	if err != nil {
		return credentials.Value{ProviderName: providerName},
			fmt.Errorf(`cannot receive session token, cause %s`, err.Error())
	}
	p.awsCredsProvider.AuthorizationToken = token
	return p.awsCredsProvider.RetrieveWithContext(ctx)
}

func (p *awsSigningHelperProvider) receiveToken(ctx context.Context) (string, error) {
	u, err := url.Parse(os.Getenv(httpProviderEnvVar))
	if err != nil {
		return "", fmt.Errorf(`env var %s must contain valid url, cause %s`, httpProviderEnvVar, err.Error())
	}

	u.Path = tokenResourcePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf(`cannot create new http request for recieving token, cause %s`, err.Error())
	}

	ttl := p.ttl
	if ttl <= 0 || ttl > sessionTokenMaxTTL {
		ttl = sessionTokenMaxTTL
	}
	req.Header.Set(sessionTokenTTLSecondsHeader, strconv.Itoa(int(ttl/time.Second)))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf(`error while recieving token, cause %s`, err.Error())
	}
	defer func(closer io.Closer) {
		_ = closer.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(`status code %d(%s) from %s, cause %s`, resp.StatusCode, http.StatusText(resp.StatusCode), u.String(), err.Error())
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf(`error while reading http response body, cause %s`, err.Error())
	}

	return string(bodyBytes), nil
}
