FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-server \
    coreutils \
    findutils \
    grep \
    sed \
    gawk \
    bash \
    procps \
    && rm -rf /var/lib/apt/lists/*

# Configure SSH
RUN mkdir /var/run/sshd \
    && echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config \
    && echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config \
    && echo 'PermitEmptyPasswords yes' >> /etc/ssh/sshd_config \
    && sed -i 's/^#\?UsePAM.*/UsePAM no/' /etc/ssh/sshd_config

# Create sandbox user with no password
RUN useradd -m -s /bin/bash sandbox \
    && passwd -d sandbox

# Generate host keys
RUN ssh-keygen -A

EXPOSE 22

CMD ["/usr/sbin/sshd", "-D", "-e"]
