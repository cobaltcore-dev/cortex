## Quickstart

### 1. Tilt Values Setup

Copy the example secrets values file. This file is used for local development and overrides the Helm chart values provided in [values.yaml](helm/cortex/values.yaml) for your local testing setup.
```bash
cp cortex.secrets.example.yaml "${HOME}/cortex.secrets.yaml"
```

> [!WARNING]
> It is recommended to put the secrets file somewhere outside of the project directory. In this way, it won't be accidentally committed to the repository.

After copying the file, fill in the necessary values.

Then, tell tilt where to find your secrets file:
```bash
export TILT_VALUES_PATH="${HOME}/cortex.secrets.yaml"
```

### 2. Running Tilt

Run the tilt setup in minikube:
```bash
minikube start && tilt up
```

Point your browser to http://localhost:10350/ - if you did everything correctly, you should see the cortex services spin up in the browser.

### 3. Simulate Nova Requests

To simulate Nova requests to your Cortex instance in Tilt, you can run the following command:
```bash
go run commands/fillup/fillup.go
```

The script will show where random new VMs would be placed.
