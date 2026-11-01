import { describe, expect, it } from 'vitest';

import type { ReplayCandle } from '../api/generated/model';
import {
  REPLAY_INTERVAL_DAYS,
  formatRemainingTime,
  formatReplayDate,
  formatReplayInterval,
  isPositiveIntegerString,
  isReplayFinishedStatus,
  latestReplayClose,
  replayDayLabel,
  replayErrorMessage,
  replayPollIntervalMs,
  replayPricePrecision,
  replayRemainingSeconds,
  validateGuestName,
} from './replay';

function candle(day: number, close: string): ReplayCandle {
  return { day, open: close, high: close, low: close, close };
}

describe('isReplayFinishedStatus', () => {
  it('finished と early_finished だけを終了として扱う', () => {
    expect(isReplayFinishedStatus('finished')).toBe(true);
    expect(isReplayFinishedStatus('early_finished')).toBe(true);
    expect(isReplayFinishedStatus('playing')).toBe(false);
  });
});

describe('replayRemainingSeconds / formatRemainingTime', () => {
  it('開始直後は標準の速さで 180 秒', () => {
    expect(replayRemainingSeconds(0, 1)).toBe(REPLAY_INTERVAL_DAYS * 3);
  });

  it('速さが上がると残り時間は短くなる', () => {
    expect(replayRemainingSeconds(0, 2)).toBe(90);
    expect(replayRemainingSeconds(0, 3)).toBe(60);
  });

  it('60 日公開済みなら 0 秒（負にならない）', () => {
    expect(replayRemainingSeconds(60, 1)).toBe(0);
    expect(replayRemainingSeconds(61, 3)).toBe(0);
  });

  it('m:ss で表示する', () => {
    expect(formatRemainingTime(180)).toBe('3:00');
    expect(formatRemainingTime(65)).toBe('1:05');
    expect(formatRemainingTime(-1)).toBe('0:00');
  });
});

describe('replayPollIntervalMs', () => {
  it('3 秒 ÷ 速さ を目安にし、1 秒より短くしない', () => {
    expect(replayPollIntervalMs(1)).toBe(3000);
    expect(replayPollIntervalMs(2)).toBe(1500);
    expect(replayPollIntervalMs(3)).toBe(1000);
    expect(replayPollIntervalMs(0)).toBe(3000);
  });
});

describe('validateGuestName', () => {
  it('前後の空白を除いて 1〜16 文字なら通す', () => {
    expect(validateGuestName('ゲスト')).toBeNull();
    expect(validateGuestName('  a  ')).toBeNull();
    expect(validateGuestName('あ'.repeat(16))).toBeNull();
  });

  it('空と 17 文字はエラー', () => {
    expect(validateGuestName('')).not.toBeNull();
    expect(validateGuestName('   ')).not.toBeNull();
    expect(validateGuestName('あ'.repeat(17))).not.toBeNull();
  });
});

describe('isPositiveIntegerString', () => {
  it('1 以上の整数だけを受け付ける', () => {
    expect(isPositiveIntegerString('1')).toBe(true);
    expect(isPositiveIntegerString('250')).toBe(true);
    expect(isPositiveIntegerString('0')).toBe(false);
    expect(isPositiveIntegerString('-1')).toBe(false);
    expect(isPositiveIntegerString('1.5')).toBe(false);
    expect(isPositiveIntegerString('abc')).toBe(false);
  });
});

describe('replayDayLabel', () => {
  it('開始前 20 日と区間の日数を区別する', () => {
    expect(replayDayLabel(0)).toBe('開始前');
    expect(replayDayLabel(19)).toBe('開始前');
    expect(replayDayLabel(20)).toBe('1日目');
    expect(replayDayLabel(79)).toBe('60日目');
  });
});

describe('latestReplayClose / replayPricePrecision', () => {
  it('最後の終値と、小数の最大桁数を返す', () => {
    const candles = [candle(0, '100.00000000'), candle(1, '108.12300000')];
    expect(latestReplayClose(candles)).toBe('108.12300000');
    expect(replayPricePrecision(candles)).toBe(3);
  });

  it('空でも落ちない', () => {
    expect(latestReplayClose([])).toBeNull();
    expect(replayPricePrecision([])).toBe(0);
  });
});

describe('formatReplayDate / formatReplayInterval', () => {
  it('年月日を日本語にする（先頭の 0 は落とす）', () => {
    expect(formatReplayDate('2020-03-02')).toBe('2020年3月2日');
    expect(formatReplayDate(null)).toBe('—');
  });

  it('同じ年なら終了日の年を省略する', () => {
    expect(formatReplayInterval('2020-03-02', '2020-05-22')).toBe(
      '2020年3月2日〜5月22日',
    );
  });

  it('年をまたぐ場合は終了日にも年を付ける', () => {
    expect(formatReplayInterval('2019-12-30', '2020-01-10')).toBe(
      '2019年12月30日〜2020年1月10日',
    );
  });
});

describe('replayErrorMessage', () => {
  it('サーバーのメッセージをそのまま使う', () => {
    expect(
      replayErrorMessage(
        { status: 409, data: { message: 'close existing positions of this pair first' } },
        '失敗しました',
      ),
    ).toBe('close existing positions of this pair first');
  });

  it('メッセージが無ければ fallback を返す', () => {
    expect(replayErrorMessage(undefined, '失敗しました')).toBe('失敗しました');
  });
});
