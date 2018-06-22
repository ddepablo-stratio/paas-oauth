package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/jose"
	"github.com/coreos/go-oidc/oauth2"
	"github.com/stratio/paas-oauth/common"
	"golang.org/x/net/context"
)

type loginRequest struct {
	Uid string `json:"uid,omitempty"`

	Password string `json:"password,omitempty"`

	Token string `json:"token,omitempty"`
}

type loginResponse struct {
	Token string `json:"token,omitempty"`
}

type profileAttributesStruct struct {
	CN string `json:"cn"`

	Mail string `json:"mail"`

	Groups []string `json:"groups"`

	Tenant string `json:"tenant"`
}

type profileStruct struct {
        Id string `json:"id"`
	Attributes []profileAttributesStruct `json:"attributes"`
}

func handleLogin(ctx context.Context, w http.ResponseWriter, r *http.Request) *common.HttpError {
	code := r.URL.Query()["code"]
	log.Printf("Code: %s", code)

	o2cli := oauth2Client(ctx)

	token, err := o2cli.RequestToken(oauth2.GrantTypeAuthCode, code[0])

	if err != nil {
		log.Print("error %w", err)
	}

	log.Printf("Token: %+v", token)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	profileUrl := ctx.Value("oauth-profile-url").(string) + token.AccessToken

	log.Printf("Getting profile: %s", profileUrl)

	resp, err := client.Get(profileUrl)

	if err != nil {
		log.Print("error %w", err)
	}

	defer resp.Body.Close()

	contents, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		log.Print("error %w", err)
	}

	log.Printf("Profile: %s", contents)

	var um profileStruct
	err = json.Unmarshal([]byte(contents), &um)

	if err != nil {
		log.Print("error %w", err)
	}

	var uid = um.Id
	var cn string
	var mail string
	var groups []string
	var tenant string

	// Look for user attributes: mail and roles
	for _, val := range um.Attributes {
		if val.CN != "" {
			cn = val.CN
		}

		if val.Mail != "" {
			mail = val.Mail
		}

		if val.Groups != nil {
			groups = val.Groups
		}

		if val.Tenant != "" {
			tenant = val.Tenant
		}
	}

	const cookieMaxAge = 3600 * 6 // 6 hours
	// required for IE 6, 7 and 8
	expiresTime := time.Now().Add(cookieMaxAge * time.Second)

	claims := jose.Claims{
		"uid": uid,
		"mail": mail,
		"cn": cn,
		"exp": expiresTime.Unix(),
		"groups": groups,
		"tenant": tenant,
	}

	secretKey, _ := ctx.Value("secret-key").([]byte)

	clusterToken, err := jose.NewSignedJWT(claims, jose.NewSignerHMAC("secret", secretKey))
	if err != nil {
		return common.NewHttpError("JWT creation error", http.StatusInternalServerError)
	}
	encodedClusterToken := clusterToken.Encode()

	domain := ctx.Value("domain").(string)
	path := ctx.Value("path").(string)

	authCookie := &http.Cookie{
		Name:     "dcos-acs-auth-cookie",
		Value:    encodedClusterToken,
		Path:     path,
		HttpOnly: true,
		Expires:  expiresTime,
		MaxAge:   cookieMaxAge,
		Secure: true,
	}

	if domain != "" {
		authCookie.Domain = domain
	}

	http.SetCookie(w, authCookie)

	user := User{
		Uid:         um.Id,
		Description: um.Id,
	}
	userBytes, err := json.Marshal(user)
	if err != nil {
		log.Printf("Marshal: %v", err)
		return common.NewHttpError("JSON marshalling failed", http.StatusInternalServerError)
	}
	infoCookie := &http.Cookie{
		Name:    "dcos-acs-info-cookie",
		Value:   base64.URLEncoding.EncodeToString(userBytes),
		Path:    path,
		Expires: expiresTime,
		MaxAge:  cookieMaxAge,
		Secure: true,
	}

        if domain != "" {
                infoCookie.Domain = domain
        }

	http.SetCookie(w, infoCookie)

        rootUrl := ctx.Value("root-url").(string)
        cookieRedirection, _ := r.Cookie("sso_redirection")
        if cookieRedirection != nil {
                rootUrl = cookieRedirection.Value
                cookieRedirection.MaxAge = -1
                cookieRedirection.Value = ""
                http.SetCookie(w, cookieRedirection)
        }
        http.Redirect(w, r, rootUrl, http.StatusFound)

	return nil
}

func handleLogout(ctx context.Context, w http.ResponseWriter, r *http.Request) *common.HttpError {
	// required for IE 6, 7 and 8
	expiresTime := time.Unix(1, 0)

	for _, name := range []string{"dcos-acs-auth-cookie", "dcos-acs-info-cookie"} {
                domain := ctx.Value("domain").(string)
                path := ctx.Value("path").(string)
		cookie := &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     path,
			HttpOnly: true,
			Expires:  expiresTime,
			MaxAge:   -1,
		}
                if domain != "" {
                        cookie.Domain = domain
                }

		http.SetCookie(w, cookie)
	}

	return nil
}

func oauth2Client(ctx context.Context) *oauth2.Client {
	key := ctx.Value("oauth-app-key").(string)
	secret := ctx.Value("oauth-app-secret").(string)
	tokenUrl := ctx.Value("oauth-token-url").(string)
	authUrl := ctx.Value("oauth-auth-url").(string)
	callbackUrl := ctx.Value("oauth-callback-url").(string)

	conf := oauth2.Config{
		Credentials: oauth2.ClientCredentials{ID: key, Secret: secret},
		TokenURL:    tokenUrl,
		AuthMethod:  oauth2.AuthMethodClientSecretBasic,
		RedirectURL: callbackUrl,
		AuthURL:     authUrl,
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	o2cli, _ := oauth2.NewClient(client, conf)
	return o2cli
}
