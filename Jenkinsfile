pipeline {
  agent any

  options {
    timestamps()
    ansiColor('xterm')
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
          ssh -o StrictHostKeyChecking=no ${DEPLOY_USER}@${DEPLOY_HOST} "mkdir -p $(dirname ${DEPLOY_PATH})"
          # Copy the binary
          scp -o StrictHostKeyChecking=no -p ${BUILD_OUT} ${DEPLOY_USER}@${DEPLOY_HOST}:${DEPLOY_PATH}
          # Ensure executable bit set
          ssh -o StrictHostKeyChecking=no ${DEPLOY_USER}@${DEPLOY_HOST} "chmod +x ${DEPLOY_PATH}"
          # Optionally trigger an immediate run (timer will handle periodic runs)
          # If systemd unit is installed on the target:
          ssh -o StrictHostKeyChecking=no ${DEPLOY_USER}@${DEPLOY_HOST} "systemctl --user daemon-reload || true"
          ssh -o StrictHostKeyChecking=no ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl daemon-reload || true"
          ssh -o StrictHostKeyChecking=no ${DEPLOY_USER}@${DEPLOY_HOST} "sudo systemctl start publicip.service || true"
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
