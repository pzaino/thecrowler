package common

import "strings"

var (
	// Lists of button texts in different languages for 'Accept' and 'Consent'
	acceptTexts = []string{
		"Accept", "Akzeptieren", "Aceptar", "Accettare", "Accetto", "Accepter", "Aceitar",
		"Godta", "Aanvaarden", "Zaakceptuj", "Elfogad", "Принять", "同意",
		"承認", "수락", // Add more translations as needed
	}
	consentTexts = []string{
		"Consent", "Zustimmen", "Consentir", "Consentire", "Consento", "Consentement", "Concordar",
		"Samtykke", "Toestemmen", "Zgoda", "Hozzájárulás", "Согласие", "同意する",
		"同意", "동의", // Add more translations as needed
	}
	rejectTexts = []string{
		"Reject", "Ablehnen", "Rechazar", "Rifiutare", "Rifiuto", "Refuser", "Rejeitar",
		"Avvise", "Weigeren", "Odrzuć", "Elutasít", "Отклонить", "拒绝",
		"拒否", "거부", // Add more translations as needed
	}
)

// ProcessEnvTemplate processes an environment variable template
func ProcessEnvTemplate(envVar, CtxID string) (EnvValue, error) {
	var rval EnvValue
	if strings.Contains(envVar, "${") {
		envVar = InterpolateEnvVars(envVar)
	}
	envVar = strings.TrimSpace(envVar)
	if strings.HasPrefix(envVar, "{{") && strings.HasSuffix(envVar, "}}") {
		envVar = strings.TrimPrefix(envVar, "{{")
		envVar = strings.TrimSuffix(envVar, "}}")
		envVar = strings.TrimSpace(envVar)
		switch envVar {
		case "accept":
			rval.Name = "accept"
			rval.Value = strings.Join(acceptTexts, "|")
			rval.Type = "string"
		case "consent":
			rval.Name = "consent"
			rval.Value = strings.Join(consentTexts, "|")
			rval.Type = "string"
		case "reject":
			rval.Name = "reject"
			rval.Value = strings.Join(rejectTexts, "|")
			rval.Type = "string"
		default:
			rIface, rProperties, err := KVStore.Get(envVar, CtxID)
			if err != nil {
				rval.Name = envVar
				rval.Value = rIface.(string)
				rval.Type = rProperties.Type
				return rval, err
			}
			rval.Name = envVar
			rval.Type = rProperties.Type
			rval.Value = rIface
		}
	}
	return rval, nil
}
