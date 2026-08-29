import { Select, type SelectProps } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { listModelConfigVersions } from '@/api/model'
import { useAuthStore } from '@/store/authStore'

type Props = Omit<SelectProps<string>, 'options' | 'loading'>

export function ModelConfigVersionSelect(props: Props) {
  const workspaceId = useAuthStore((state) => state.workspaceId)
  const versions = useQuery({
    queryKey: ['model-config-versions', workspaceId],
    queryFn: listModelConfigVersions,
    enabled: !!workspaceId,
  })

  return (
    <Select
      placeholder="选择不可变模型配置版本"
      {...props}
      loading={versions.isLoading}
      options={(versions.data ?? []).map((item) => ({
        value: item.id,
        label: `${item.name} / v${item.version} / ${item.model_name}`,
      }))}
    />
  )
}
