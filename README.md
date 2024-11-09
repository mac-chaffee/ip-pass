# ip-pass

Allow-list your IP by merely visiting a website. Ideal for side projects and home labs.

The main use-case is sharing self-hosted services with people (like distant friends and family) who can't install a VPN client or use a password manager. They can visit ip-pass, click a button, and gain access.

Currently only supports allow-listing via Traefik Middleware custom resources: <https://doc.traefik.io/traefik/middlewares/http/ipallowlist>

## Security

You should think of ip-pass as the equivalent of moving your SSH port to something other than 22: It will prevent automated scanners from hitting your server, but provides no protection against a malicious person. It's security by obscurity.

## Usage

Start the server, which will create a Middleware if one does not exist:
```
$ go run pkg/main.go
{"level":"info","ts":"2024-11-08T22:17:04-05:00","logger":"entrypoint","msg":"Created Middleware","name":"ip-allowlist","namespace":"default"}
{"level":"info","ts":"2024-11-08T22:17:04-05:00","logger":"entrypoint","msg":"Starting server","addr":":8080"}
```
```
$ kubectl get middlewares.traefik.io -A
NAMESPACE   NAME             AGE
default     ip-allowlist     12s
```

Allow-list an IP:

```
$ curl -iX PUT -H "X-Forwarded-For: 2001:0db8::123" localhost:8080/add-ip
HTTP/1.1 201 Created
Date: Sat, 09 Nov 2024 03:21:40 GMT
Content-Length: 0
```

Verify that the IP was allow-listed (we mask all IPs to the nearest /64 for IPv6 or /24 for IPv4):

```
$ kubectl get middlewares.traefik.io ip-allowlist -n mealie -o yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  creationTimestamp: "2024-11-09T03:17:04Z"
  generation: 2
  name: ip-allowlist
  namespace: default
  resourceVersion: "12075625"
  uid: 012d7815-20e0-476e-a9a2-6f52c2dd16ce
spec:
  ipWhiteList:
    sourceRange:
    - 2001:db8::/64
```

## Production-readiness

Do not use this in production. Expect bug reports and PRs to be neglected.

## Development

Must have Golang installed. Must install [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports).

Please set up a pre-commit hook by running this command:

```
cat > .git/hooks/pre-commit << EOF
#!/usr/bin/env bash
set -Eeuo pipefail
cd \$(git rev-parse --show-toplevel)
go fmt ./pkg
goimports -w ./pkg
go test ./pkg
EOF
chmod +x .git/hooks/pre-commit
```

### Releasing

```
echo $GH_PAT | docker login ghcr.io -u mac-chaffee --password-stdin

TAG=v1.0.0

git tag $TAG
export DOCKER_DEFAULT_PLATFORM=linux/amd64
docker build . -t ghcr.io/mac-chaffee/ip-pass:$TAG
docker push ghcr.io/mac-chaffee/ip-pass:$TAG
git push origin --tags
```

### Installation

```
$ kubectl apply -k ./k8s/
namespace/ip-pass created
serviceaccount/ip-pass created
role.rbac.authorization.k8s.io/traefik-middleware-editor created
rolebinding.rbac.authorization.k8s.io/traefik-middleware-editor-binding created
service/ip-pass created
deployment.apps/ip-pass created
```
