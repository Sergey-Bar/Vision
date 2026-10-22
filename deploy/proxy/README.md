# The reference authenticating proxy.
#
# # What this is for
#
# `docs/decisions.md` D-16 is the appliance's auth posture: the console is
# protected at the NETWORK boundary. A trusted reverse proxy terminates TLS,
# authenticates the operator, and injects an identity header. The console
# believes the header only when the request arrives from a trusted peer.
#
# This is the configuration that makes that true. It is a REFERENCE, not a
# product: the product does not ship a proxy, and the point of shipping a
# reference is that the operator can read what the product BELIEVES and
# compare it with what their proxy DOES.
#
# # The three things the product believes, and the three things this file does
#
# | the product believes | this file must |
# |---|---|
# | the identity header came from the proxy | STRIP any client-supplied copy before setting it |
# | the request came from the proxy | be the ONLY thing that can reach the console's port |
# | the operator is who the proxy says | authenticate the operator, and set the header from THAT |
#
# The first row is the one a deployment gets wrong. A proxy that appends the
# header to a client-supplied one lets anybody who can reach the proxy claim to
# be anybody, and the console's refusal of an untrusted peer is the only thing
# that would notice -- and the peer IS trusted, so it would not.
#
# # The header the product reads
#
# `SP_TRUST_PROXY_AUTH_HEADER` names it. This file uses `X-Auth-Request-User`,
# which is what `oauth2-proxy` and `Caddy`'s `forward_auth` set. The name is
# configuration, so the operator can use their proxy's own name.
#
# # The product's configuration that goes with it
#
# SP_BEHIND_PROXY=true
# SP_TRUSTED_PROXY_CIDRS=127.0.0.1/32
# SP_TRUST_PROXY_AUTH_HEADER=X-Auth-Request-User
# SP_LISTEN_ADDR=127.0.0.1:8443
#
# The console binds LOOPBACK, so the only thing that can reach it is the proxy
# on the same machine. That is what makes the header unforgeable: a header is
# forgeable by anybody who can reach the port, and this is a port only the proxy
# can reach.
#
# Without a proxy, the console refuses to serve a non-loopback address at all
# (`internal/config` refuses the configuration), because every operator who
# could reach the port would be the same configured principal.

# ---------------------------------------------------------------------------
# Caddy
# ---------------------------------------------------------------------------
#
# Caddy's `forward_auth` asks another service who the operator is and copies
# the answer into the request. The `header_upstream` line is the one that
# matters: it REPLACES the header rather than appending to it, so a client
# cannot smuggle one in.

{
	admin off
	auto_https off
}

:8443 {
	# TLS is terminated HERE, by the proxy, not by the product. The product
	# listens on loopback in plaintext and trusts the socket.
	tls internal

	# Who is the operator. The `X-Auth-Request-User` header is SET by this
	# directive, from the authenticated session, and REPLACES anything the
	# client sent.
	forward_auth 127.0.0.1:9000 {
		uri /verify
		copy_headers X-Auth-Request-User X-Auth-Request-Email X-Auth-Request-Groups

		# The product reads the operator's NAME. The groups are the operator's
		# OWN assertion and the product does not read them: the role and the
		# site scope are the product's configuration, so a proxy that could
		# grant capabilities would be a second authorisation system.
		header_upstream X-Auth-Request-User {>http.auth.user.id}
	}

	# The product's own port. Loopback, so nothing else can reach it.
	reverse_proxy 127.0.0.1:8443 {
		header_upstream X-Auth-Request-User {http.request.header.X-Auth-Request-User}
		header_upstream X-Forwarded-For {http.request.remote.host}
	}

	log {
		output stdout
		format console
	}
}

# ---------------------------------------------------------------------------
# nginx
# ---------------------------------------------------------------------------
#
# The nginx form is the same three things with different names. The line that
# does the work is `proxy_set_header X-Auth-Request-User $remote_user;` --
# nginx SETS it from the authenticated user, and `proxy_set_header` REPLACES
# rather than appends, so a client-supplied copy is discarded.

# server {
#     listen 443 ssl;
#     server_name console.example.internal;
#
#     ssl_certificate     /etc/ssl/console.crt;
#     ssl_certificate_key /etc/ssl/console.key;
#
#     # The identity the product reads. nginx sets it from the authenticated
#     # user; `proxy_set_header` replaces, so a client-supplied copy is gone.
#     auth_request /auth;
#     proxy_set_header X-Auth-Request-User $remote_user;
#     proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
#
#     # The product's own port. Loopback, so nothing else can reach it.
#     location / {
#         proxy_pass http://127.0.0.1:8443;
#         proxy_set_header Host $host;
#         proxy_set_header X-Forwarded-Proto $scheme;
#     }
#
#     location /auth {
#         internal;
#         proxy_pass http://127.0.0.1:9000/verify;
#         proxy_set_header X-Original-URI $request_uri;
#     }
# }
