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
        sh 'file ${BUILD_OUT} || true'
      }
    }

    stage('Deploy') {
      steps {
        sh '''
          set -euo pipefail
          # Ensure target directories exist
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mkdir -p $(dirname ${DEPLOY_PATH}) && sudo chown ${DEPLOY_USER} $(dirname ${DEPLOY_PATH})"
          # Copy the binary
          scp -p ${BUILD_OUT} ${DEPLOY_USER}@${DEPLOY_HOST}:${DEPLOY_PATH}
          # Ensure executable bit set
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "chmod +x ${DEPLOY_PATH}"
          # Install/Update system-wide systemd unit and timer
          scp -p systemd/publicip.service systemd/publicip.timer ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mv /tmp/publicip.service /etc/systemd/system/publicip.service && sudo mv /tmp/publicip.timer /etc/systemd/system/publicip.timer"
          # Ensure environment directory exists and seed env file if absent
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo mkdir -p /etc/cli-things"
          scp -p systemd/publicip.conf.sample ${DEPLOY_USER}@${DEPLOY_HOST}:/tmp/publicip.conf.sample
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "if [ ! -f /etc/cli-things/publicip.conf ]; then sudo mv /tmp/publicip.conf.sample /etc/cli-things/publicip.conf; else sudo rm -f /tmp/publicip.conf.sample; fi"
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl daemon-reload"
          # Enable and start the timer (system-wide)
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl enable --now publicip.timer"
          # Optionally start the service immediately once
          ssh ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl start publicip.service || true"
        '''
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
      archiveArtifacts artifacts: 'bin/publicip', allowEmptyArchive: true
    }
  }
}
