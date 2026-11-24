import { ElMessage } from 'element-plus';

export function operationMessage(message: string, type: 'success' | 'warning' | 'error' | 'info' = 'success') {
    ElMessage({ message, type, placement: 'bottom-right', showClose: true, grouping: true, duration: type === 'success' ? 3500 : 6500 });
}
