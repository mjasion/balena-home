FROM nginx:1.28.0-alpine

COPY nginx.conf /etc/nginx/nginx.conf
COPY web* /etc/nginx/conf.d/

RUN rm /etc/nginx/conf.d/default.conf

EXPOSE 80 443 8080 8443
