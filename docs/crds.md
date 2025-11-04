# Custom Resource Definitions (CRDs) 

## All Cortex owned CRDs
### Datasource CRD
 - **Purpose**: Defines external data sources and sync schedules, continuously sync infrastructure state from multiple services (OpenStack, Prometheus, etc.)
 - **Event Trigger**: Scheduled sync intervals or external webhooks
 - **Owner**: Knowledge Operator (service-specific reconcilers)
 
### Knowledge CRD
 - **Purpose**: Stores extracted features, Transforms raw data into actionable scheduling knowledge
 - **Event Trigger**: Datasource updates or dependency changes
 - **Owner**: Knowledge Operator (KnowledgeReconciler, TriggerReconciler)

### Decision CRD
 - **Purpose**: Records scheduling requests, decisions, and explanations
 - **Event Trigger**: External scheduler calls from various services
 - **Owner**: Scheduling Operator (service-specific controllers)

### Pipeline CRD
 - **Purpose**: Defines scheduling workflows, configures multi-step scheduling logic per bundle
 - **Event Trigger**: Configuration changes
 - **Owner**: Scheduling Operator (DeschedulingsPipelineController, DecisionPipelineController variants)

### Step CRD
 - **Purpose**: Track individual steps within pipelines (filters, weighers, etc.)
 - **Event Trigger**: Pipeline configuration changes
 - **Owner**: Scheduling Operator

### Descheduling CRD
 - **Purpose**: Recommendations for moving workloads
 - **Event Trigger**: Capacity events
 - **Owner**: Scheduling Operator (Cleanup controllers)

### Reservation CRD
 - **Purpose**: Manages capacity reservations, reserve resources
 - **Event Trigger**: Capacity requests
 - **Owner**: Reservations Operator (ReservationReconciler)


## Simplified Overview

```mermaid
graph TD
    subgraph "External Systems"
        EXT[OpenStack Schedulers<br/>Nova, Cinder, Manila]
        DATA[Data Sources<br/>Prometheus, APIs]
    end

    subgraph "Cortex"
        subgraph "Interface Layer"
            SHIMS[Cortex HTTP Shims]
        end
        
        subgraph "State/Persistency Layer"
            DS[(Datasource CRD)]
            K[(Knowledge CRD)]
            D[(Decision CRD)]
            R[(Reservation CRD)]
            P[(Pipeline CRD)]
        end
        
        subgraph "Processing Layer"
            CTRL((Controllers))
        end
    end
    
    %% Main flows
    EXT -->|scheduling requests| SHIMS
    CTRL -->|fetch from| DATA
    
    SHIMS -->|creates| D
    CTRL -->|updates| DS
    DS -->|triggers| K
    K -->|informs| D
    P -.->|configures| D
    R -.->|configures| D
    
    CTRL -->|processes| DS
    CTRL -->|processes| K
    CTRL -->|processes| D
    
    D -->|results| SHIMS
    SHIMS -->|responses| EXT
    
    %% Styling
    classDef external fill:#ECEFF1,stroke:#607D8B,stroke-width:3px,color:#37474F
    classDef cortexShim fill:#E1F5FE,stroke:#0277BD,stroke-width:2px,color:#01579B
    classDef knowledgeCrd fill:#E3F2FD,stroke:#2196F3,stroke-width:2px,color:#1976D2
    classDef schedulingCrd fill:#E8F5E8,stroke:#4CAF50,stroke-width:2px,color:#2E7D32
    classDef controller fill:#F3E5F5,stroke:#9C27B0,stroke-width:2px,color:#7B1FA2
    
    class EXT,DATA external
    class SHIMS cortexShim
    class DS,K,P knowledgeCrd
    class D,R schedulingCrd
    class CTRL controller
```

## CRD Ownership and Bundle Context

Operators can be deployed together in bundles (e.g., Nova bundle) or separately. 

```mermaid
graph TB
    subgraph "Bundle Deployment Nova"
        subgraph "Knowledge Operator"
            KO{{Knowledge Operator}}
            KO -.->|deploys| DSR{DatasourceReconciler}
            KO -.->|deploys| KR{KnowledgeReconciler}
            KO -.->|deploys| TR{TriggerReconciler}
            
            DSR -->|updates status| DS(Datasource CRDs)
            KR -->|updates status| K(Knowledge CRDs)
            TR -->|watches & triggers| K
        end
        
        subgraph "Scheduling Operator"
            SO{{Scheduling Operator}}
            SO -.->|deploys| DPC{DecisionPipelineController}
            SO -.->|deploys| EX{ExplanationController}
            
            DPC -->|updates status| D(Decision CRDs)
            DPC -->|updates status| P(Pipeline CRDs)
            EX -->|updates status| D
        end
        
        subgraph "Reservations Operator"
            RO{{Reservations Operator}}
            RO -.->|deploys| RR{ReservationReconciler}
            
            RR -->|updates status| R(Reservation CRDs)
        end
    end
    
    subgraph "Cortex HTTP Shims"
        NOVASHIM[Nova Shim]
        CINDERSHIM[Cinder Shim]
        MANILASHIM[Manila Shim]
        IRONCORE[IronCore]
    end
    
    subgraph "External Systems"
        EXTNOVА[OpenStack Nova]
        EXTCINDER[OpenStack Cinder]
        EXTMANILA[OpenStack Manila]
        PROM[Prometheus]
        OS[OpenStack APIs]
    end
    
    %% External to Shims
    EXTNOVА -->|HTTP requests| NOVASHIM
    EXTCINDER -->|HTTP requests| CINDERSHIM
    EXTMANILA -->|HTTP requests| MANILASHIM
    
    %% Shims create CRD specs
    NOVASHIM -->|creates spec| D
    CINDERSHIM -->|creates spec| D
    MANILASHIM -->|creates spec| D
    IRONCORE -->|creates spec| D
    
    %% Controllers fetch from external sources
    DSR -->|fetches from| PROM
    DSR -->|fetches from| OS
    
    %% CRD relationships
    DS -->|completion triggers| K
    K -.->|referenced by| D
    R -.->|referenced by| D
    P -->|referenced by| D
    
    %% Styling
    classDef operator fill:#2196F3,stroke:#1976D2,stroke-width:3px,color:#fff
    classDef knowledgeCrd fill:#E3F2FD,stroke:#2196F3,stroke-width:2px,color:#1976D2
    classDef schedulingCrd fill:#E8F5E8,stroke:#4CAF50,stroke-width:2px,color:#2E7D32
    classDef reservationCrd fill:#FFF3E0,stroke:#FF9800,stroke-width:2px,color:#F57C00
    classDef controller fill:#F3E5F5,stroke:#9C27B0,stroke-width:2px,color:#7B1FA2
    classDef cortexShim fill:#E1F5FE,stroke:#0277BD,stroke-width:2px,color:#01579B
    classDef external fill:#ECEFF1,stroke:#607D8B,stroke-width:3px,color:#37474F
    
    class KO,SO,RO operator
    class DS,K knowledgeCrd
    class D,P schedulingCrd
    class R reservationCrd
    class DSR,KR,TR,DPC,EX,RR controller
    class NOVASHIM,CINDERSHIM,MANILASHIM,IRONCORE cortexShim
    class EXTNOVА,EXTCINDER,EXTMANILA,PROM,OS external
```

**Legend:**
- 🔷 **Hexagons**: Cortex Operators
- 🔘 **Rounded Rectangles**: CRDs 
- 🔶 **Diamonds**: Controllers/Reconcilers
- **Rectangles (Light Blue)**: Cortex HTTP Shims
- **Rectangles (Gray)**: External Systems
- **Solid arrows**: Direct actions (creates, updates, triggers)
- **Dotted arrows**: Management/reference relationships

