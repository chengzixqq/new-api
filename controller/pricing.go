package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// applyPersonalGroupPricingRatios keeps the pricing response aligned with the
// runtime precedence without mutating model.GetPricing's shared cached maps.
func applyPersonalGroupPricingRatios(pricing []model.Pricing, userOverrides map[string]float64) []model.Pricing {
	if len(pricing) == 0 || len(userOverrides) == 0 {
		return pricing
	}

	adjusted := make([]model.Pricing, len(pricing))
	copy(adjusted, pricing)
	for i := range adjusted {
		if len(adjusted[i].GroupPricing) == 0 {
			continue
		}

		var groupPricingCopy map[string]types.ModelGroupPricing
		for group, ratio := range userOverrides {
			groupPricing, exists := adjusted[i].GroupPricing[group]
			if !exists || !model.IsValidGroupRatioOverride(ratio) {
				continue
			}
			if groupPricingCopy == nil {
				groupPricingCopy = make(map[string]types.ModelGroupPricing, len(adjusted[i].GroupPricing))
				for name, value := range adjusted[i].GroupPricing {
					groupPricingCopy[name] = value
				}
			}
			ratioCopy := ratio
			groupPricing.Ratio = &ratioCopy
			if groupPricing.ModelPrice != nil {
				value := *groupPricing.ModelPrice * ratio
				groupPricing.ModelPrice = &value
			}
			if groupPricing.PromptPrice != nil {
				value := *groupPricing.PromptPrice * ratio
				groupPricing.PromptPrice = &value
			}
			if groupPricing.CompletionPrice != nil {
				value := *groupPricing.CompletionPrice * ratio
				groupPricing.CompletionPrice = &value
			}
			if groupPricing.CachePrice != nil {
				value := *groupPricing.CachePrice * ratio
				groupPricing.CachePrice = &value
			}
			if groupPricing.CreateCachePrice != nil {
				value := *groupPricing.CreateCachePrice * ratio
				groupPricing.CreateCachePrice = &value
			}
			if groupPricing.ImagePrice != nil {
				value := *groupPricing.ImagePrice * ratio
				groupPricing.ImagePrice = &value
			}
			if groupPricing.AudioPrice != nil {
				value := *groupPricing.AudioPrice * ratio
				groupPricing.AudioPrice = &value
			}
			if groupPricing.AudioCompletionPrice != nil {
				value := *groupPricing.AudioCompletionPrice * ratio
				groupPricing.AudioCompletionPrice = &value
			}
			if groupPricing.MinFee != nil {
				value := *groupPricing.MinFee * ratio
				groupPricing.MinFee = &value
			}
			groupPricingCopy[group] = groupPricing
		}
		if groupPricingCopy != nil {
			adjusted[i].GroupPricing = groupPricingCopy
		}
	}
	return adjusted
}

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

// adminUsableGroups 返回管理员在模型广场应看到的全部分组：所有可计费分组
// （即出现在 groupRatio 中的分组，含非用户可选分组）+ 已配置的用户可选分组
// + 管理员自身分组。已配置的分组描述会被保留；仅在倍率表中出现的分组以分组名
// 作为兜底描述。入参 map 不会被修改。
func adminUsableGroups(configured map[string]string, groupRatio map[string]float64, userGroup string) map[string]string {
	result := make(map[string]string, len(configured)+len(groupRatio)+1)
	for g, desc := range configured {
		result[g] = desc
	}
	for g := range groupRatio {
		if _, ok := result[g]; !ok {
			result[g] = g
		}
	}
	if userGroup != "" {
		if _, ok := result[userGroup]; !ok {
			result[userGroup] = userGroup
		}
	}
	return result
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	var userOverrides map[string]float64
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
			userOverrides = user.GetGroupRatioOverrides()
			for targetGroup, ratio := range userOverrides {
				if _, ok := groupRatio[targetGroup]; ok {
					groupRatio[targetGroup] = ratio
				}
			}
		}
	}
	pricing = applyPersonalGroupPricingRatios(pricing, userOverrides)

	usableGroup = service.GetUserUsableGroups(group)
	if exists {
		if userIdInt, ok := userId.(int); ok && model.IsAdmin(userIdInt) {
			// 管理员可查看所有可计费分组的价格（含非用户可选的受限分组，如仅 c 分组账号可选的 c 分组），
			// 不应被自身账号分组的可选范围限制。
			usableGroup = adminUsableGroups(usableGroup, groupRatio, group)
		}
	}
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
