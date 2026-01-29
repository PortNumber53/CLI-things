def INSTALL_TARGETS = [
  'dbtool'           : ['book14', 'book16', 'brain', 'crash', 'pinky', 'zenbook'],
  'cloudflare-backup': ['crash'],
  'internalip'       : ['brain', 'crash', 'pinky', 'zenbook'],
]

// Optional per-host SSH port overrides (defaults to 22 when not specified)
def HOST_SSH_PORTS = [
  'brain' : '22040',
  'pinky' : '22050',
  'zenbook': '22070',
]

def HOST_SSH_USERS = ['book14': 'mauricio']

pipeline {
  agent any

  options {
    timestamps()
  }

  environment {
    // Adjust if Jenkins requires a specific Go version tool, e.g., 'go-1.21'
    // PATH = "/usr/local/go/bin:${env.PATH}"
    BINARY_NAME = 'publicip'
    BUILD_DIR   = 'utility/publicip'
    BUILD_OUT   = 'bin/publicip'

    CF_BINARY_NAME = 'cloudflare-backup'
    CF_BUILD_DIR   = 'utility/cloudflare-backup'
    CF_BUILD_OUT   = 'bin/cloudflare-backup'

    DBTOOL_BUILD_DIR = 'utility/dbtool'
    DBTOOL_BUILD_OUT = 'bin/dbtool'

    INTERNALIP_BINARY_NAME = 'internalip'
    INTERNALIP_BUILD_DIR   = 'utility/internalip'
    INTERNALIP_BUILD_OUT   = 'bin/internalip'

    DEPLOY_HOST = 'crash'
    DEPLOY_USER = 'grimlock'
    DEPLOY_PATH = '/opt/cli-things/bin/publicip'
  }

  stages {
    stage('Checkout') {
      steps {
        checkout scm
      }
    }

    stage('Build') {
      steps {
        sh 'go version || true'
        sh 'go mod download'
        sh 'go build -o ${BUILD_OUT} ./${BUILD_DIR}'
        sh 'go build -o ${CF_BUILD_OUT} ./${CF_BUILD_DIR}'
        sh 'go build -o ${INTERNALIP_BUILD_OUT} ./${INTERNALIP_BUILD_DIR}'
        // Build dbtool using its dedicated main with the dbtool build tag,
        // so we get an executable binary (not a package archive).
        sh 'go build -tags dbtool -o ${DBTOOL_BUILD_OUT} ./dbtool.go'
        sh 'GOOS=darwin GOARCH=arm64 go build -tags dbtool -o bin/dbtool-arm64 ./dbtool.go'  // Add arm64 build for Apple Silicon
        sh 'file ${BUILD_OUT} || true'
        sh 'file ${CF_BUILD_OUT} || true'
        sh 'file ${INTERNALIP_BUILD_OUT} || true'
        sh 'file ${DBTOOL_BUILD_OUT} || true'
        sh 'file bin/dbtool-arm64 || true'
      }
    }

    stage('Deploy') {
      steps {
        sh '''
          set -euo pipefail
          # Ensure target directories exist
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mkdir -p $(dirname ${DEPLOY_PATH})"
          # Copy binaries to /tmp first, then sudo move into place (avoids scp failures when destination is root-owned)
          scp -p ${BUILD_OUT} ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/publicip
          scp -p ${INTERNALIP_BUILD_OUT} ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/internalip
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mv /tmp/publicip ${DEPLOY_PATH} && sudo chmod +x ${DEPLOY_PATH}"
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mv /tmp/internalip /opt/cli-things/bin/internalip && sudo chmod +x /opt/cli-things/bin/internalip"
          # Install/Update system-wide systemd unit and timers on primary host
          scp -p systemd/publicip.service systemd/publicip.timer \
                 systemd/publicip-collect.service systemd/publicip-collect.timer \
                 systemd/publicip-sync.service systemd/publicip-sync.timer \
                 systemd/cloudflare-backup.service systemd/cloudflare-backup.timer \
                 systemd/cloudflare-backup.conf.sample \
                 utility/internalip/internalip-capture.service utility/internalip/internalip-capture.timer \
                 ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mv /tmp/publicip.service /etc/systemd/system/publicip.service && \
                                             sudo mv /tmp/publicip.timer /etc/systemd/system/publicip.timer && \
                                             sudo mv /tmp/publicip-collect.service /etc/systemd/system/publicip-collect.service && \
                                             sudo mv /tmp/publicip-collect.timer /etc/systemd/system/publicip-collect.timer && \
                                             sudo mv /tmp/publicip-sync.service /etc/systemd/system/publicip-sync.service && \
                                             sudo mv /tmp/publicip-sync.timer /etc/systemd/system/publicip-sync.timer && \
                                             sudo mv /tmp/cloudflare-backup.service /etc/systemd/system/cloudflare-backup.service && \
                                             sudo mv /tmp/cloudflare-backup.timer /etc/systemd/system/cloudflare-backup.timer && \
                                             sudo mv /tmp/internalip-capture.service /etc/systemd/system/internalip-capture.service && \
                                             sudo mv /tmp/internalip-capture.timer /etc/systemd/system/internalip-capture.timer"
          # Ensure environment directory exists and seed env file if absent
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mkdir -p /etc/cli-things && sudo mkdir -p /etc/cloudflare-backup"
          scp -p systemd/publicip.conf.sample ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/publicip.conf.sample
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "if [ ! -f /etc/cli-things/publicip.conf ]; then sudo mv /tmp/publicip.conf.sample /etc/cli-things/publicip.conf; else sudo rm -f /tmp/publicip.conf.sample; fi"
          scp -p systemd/cloudflare-backup.conf.sample ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/cloudflare-backup.conf.sample
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "if [ ! -f /etc/cloudflare-backup/config.conf ]; then sudo mv /tmp/cloudflare-backup.conf.sample /etc/cloudflare-backup/config.conf; else sudo rm -f /tmp/cloudflare-backup.conf.sample; fi"
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl daemon-reload"
          # Run one of the utilities once so shared DB migrations are applied
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "/opt/cli-things/bin/cloudflare-backup --timeout=10s || true"
          # Enable and start the timers (system-wide)
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl enable --now publicip.timer publicip-collect.timer publicip-sync.timer cloudflare-backup.timer internalip-capture.timer"
          # Optionally start the service immediately once
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl start publicip.service || true"
        '''
      }
    }

    stage('Deploy cf-cli') {
      steps {
        script {
          def cfHosts = INSTALL_TARGETS['cloudflare-backup'] ?: []
          for (host in cfHosts) {
            def port = HOST_SSH_PORTS[host] ?: '22'
            def user = HOST_SSH_USERS.get(host, 'grimlock')
            sh """
              set -euo pipefail
              ssh -p ${port} ${user}@${host} "sudo mkdir -p /opt/cli-things/bin"
              scp -P ${port} -p ${CF_BUILD_OUT} ${user}@${host}:/tmp/cloudflare-backup
              ssh -p ${port} ${user}@${host} "sudo mv /tmp/cloudflare-backup /opt/cli-things/bin/cloudflare-backup && sudo chmod +x /opt/cli-things/bin/cloudflare-backup"
            """
          }
        }
      }
    }

    stage('Deploy dbtool') {
      steps {
        script {
          def dbtoolHosts = INSTALL_TARGETS['dbtool'] ?: []
          for (host in dbtoolHosts) {
            def port = HOST_SSH_PORTS[host] ?: '22'
            def user = HOST_SSH_USERS.get(host, 'grimlock')
            def archBinary = (host in ['book14', 'book16'] ? 'dbtool-arm64' : 'dbtool')
              sh """
                set -euo pipefail
                ssh -p ${port} ${user}@${host} "sudo mkdir -p /usr/local/bin"
                scp -P ${port} -p bin/${archBinary} ${user}@${host}:/tmp/dbtool
                ssh -p ${port} ${user}@${host} "sudo mv /tmp/dbtool /usr/local/bin/dbtool && sudo chmod +x /usr/local/bin/dbtool"
              """
            """
          }
        }
      }
    }

    stage('Deploy internalip') {
      steps {
        script {
          def internalipHosts = INSTALL_TARGETS['internalip'] ?: []
          for (host in internalipHosts) {
            def port = HOST_SSH_PORTS[host] ?: '22'
            def user = HOST_SSH_USERS.get(host, 'grimlock')
            sh """
              set -euo pipefail
              ssh -p ${port} ${user}@${host} "sudo mkdir -p /opt/cli-things/bin"
              scp -P ${port} -p ${INTERNALIP_BUILD_OUT} ${user}@${host}:/tmp/internalip
              ssh -p ${port} ${user}@${host} "sudo mv /tmp/internalip /opt/cli-things/bin/internalip && sudo chmod +x /opt/cli-things/bin/internalip"
              # Install systemd service and timer for internal IP capture
              scp -P ${port} -p utility/internalip/internalip-capture.service utility/internalip/internalip-capture.timer ${user}@${host}:/tmp/
              ssh -p ${port} ${user}@${host} "sudo mv /tmp/internalip-capture.service /etc/systemd/system/internalip-capture.service && \
                                                     sudo mv /tmp/internalip-capture.timer /etc/systemd/system/internalip-capture.timer && \
                                                     sudo systemctl daemon-reload && \
                                                     sudo systemctl enable --now internalip-capture.timer"
            """
          }
        }
      }
    }
  }

  post {
    success {
      echo 'Deployment completed successfully.'
    }
    failure {
      echo 'Deployment failed.'
    }
    always {
      archiveArtifacts artifacts: 'bin/publicip,bin/internalip', allowEmptyArchive: true
    }
  }
}
