FROM php:8.4-apache

# Serve files under /neo/ path to match get.vxero.dev/neo/ URLs
COPY . /var/www/html/neo/

# Allow .htaccess overrides and directory index
RUN echo '<Directory /var/www/html>\n\
    Options Indexes FollowSymLinks\n\
    AllowOverride All\n\
    Require all granted\n\
</Directory>' > /etc/apache2/conf-available/neo-site.conf \
    && a2enconf neo-site \
    && a2enmod rewrite

EXPOSE 80
