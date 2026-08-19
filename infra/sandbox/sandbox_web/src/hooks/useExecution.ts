/**
 * Execution management hook
 */
import { useState, useCallback } from 'react';
import { message } from 'antd';
import * as executionsApi from '@apis/executions';
import type {
  ExecutionResponse,
  ExecuteCodeRequest,
  ExecuteCodeResponse,
} from '@apis/executions';

export function useExecution() {
  const [executions, setExecutions] = useState<ExecutionResponse[]>([]);
  const [loading, setLoading] = useState(false);
  const [currentExecution, setCurrentExecution] = useState<ExecutionResponse | null>(null);

  // Submit code execution
  const executeCode = useCallback(async (
    sessionId: string,
    data: ExecuteCodeRequest
  ): Promise<ExecuteCodeResponse> => {
    setLoading(true);
    try {
      const result = await executionsApi.executeCode(sessionId, data);
      // Poll for execution results
      pollExecutionResult(result.execution_id);
      return result;
    } catch (error) {
      message.error('代码执行提交失败');
      console.error(error);
      throw error;
    } finally {
      setLoading(false);
    }
  }, []);

  // Poll execution results
  const pollExecutionResult = useCallback(async (executionId: string) => {
    const maxAttempts = 60; // Poll at most 60 times, about 1 minute.
    let attempts = 0;

    const poll = async () => {
      attempts++;
      try {
        const result = await executionsApi.getExecutionResult(executionId);
        setCurrentExecution(result);

        // Continue polling if execution is still in progress
        if (
          result.status === 'pending' ||
          result.status === 'PENDING' ||
          result.status === 'running' ||
          result.status === 'RUNNING'
        ) {
          if (attempts < maxAttempts) {
            setTimeout(poll, 1000); // Retry after 1 second.
          }
        } else {
          // When execution completes, add it to history and clear the current execution
          setExecutions((prev) => {
            // Check whether it already exists to avoid duplicate additions
            const exists = prev.some(e => e.id === result.id);
            if (exists) {
              return prev;
            }
            return [result, ...prev];
          });
          // Delay clearing currentExecution so the user can see the final state
          setTimeout(() => setCurrentExecution(null), 100);
          if (result.status === 'completed' || result.status === 'COMPLETED') {
            message.success('代码执行成功');
          } else if (result.status === 'failed') {
            message.error('代码执行失败');
          } else if (result.status === 'timeout') {
            message.error('代码执行超时');
          }
        }
      } catch (error) {
        console.error('获取执行结果失败', error);
        if (attempts < maxAttempts) {
          setTimeout(poll, 1000);
        }
      }
    };

    poll();
  }, []);

  // Get the execution list for a session
  const fetchSessionExecutions = useCallback(async (sessionId: string) => {
    setLoading(true);
    try {
      const result = await executionsApi.listSessionExecutions(sessionId);
      setExecutions(result.items);
    } catch (error) {
      message.error('获取执行历史失败');
      console.error(error);
    } finally {
      setLoading(false);
    }
  }, []);

  // Clear current execution
  const clearCurrentExecution = useCallback(() => {
    setCurrentExecution(null);
  }, []);

  return {
    executions,
    currentExecution,
    loading,
    executeCode,
    fetchSessionExecutions,
    clearCurrentExecution,
  };
}
