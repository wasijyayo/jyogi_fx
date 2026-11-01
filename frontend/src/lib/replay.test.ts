import { describe, expect, it } from 'vitest';

import type { ReplayCandle } from '../api/generated/model';
import {
  REPLAY_INTERVAL_DAYS,
  REPLAY_MIN_POLL_MS,
  REPLAY_POLL_MARGIN_MS,
  canReplayOrder,
  clampReplayLeverage,
  clampReplayMargin,
  formatRemainingTime,
  formatReplayDate,
  formatReplayInterval,
  isPositiveAmount,
  isPositiveIntegerString,
  isReplayFinishedStatus,
  latestReplayClose,
  replayCountdownValue,
  replayDayLabel,
  replayErrorMessage,
  replayMaxMargin,
  replayPollDelayMs,
  replayPollIntervalMs,
  replayPricePrecision,
  replayRemainingSeconds,
  stepReplayLeverage,
  stepReplayMargin,
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

describe('replayPollDelayMs', () => {
  it('次の日が公開される時刻に余白を足して取りに行く', () => {
    expect(replayPollDelayMs(3000, 0, 1)).toBe(3000 + REPLAY_POLL_MARGIN_MS);
    expect(replayPollDelayMs(1500, 500, 2)).toBe(1000 + REPLAY_POLL_MARGIN_MS);
  });

  it('取得してから時間が経っていれば待ち時間を短くする', () => {
    expect(replayPollDelayMs(1000, 950, 3)).toBe(REPLAY_MIN_POLL_MS);
  });

  it('msUntilNextDay が 0 なら速さから決まる通常の間隔にする', () => {
    expect(replayPollDelayMs(0, 0, 1)).toBe(3000);
    expect(replayPollDelayMs(0, 0, 3)).toBe(1000);
  });

  it('通常の間隔より長くは待たない', () => {
    expect(replayPollDelayMs(60_000, 0, 3)).toBe(1000 + REPLAY_POLL_MARGIN_MS);
  });
});

describe('isPositiveAmount', () => {
  it('正の decimal 文字列だけを true にする', () => {
    expect(isPositiveAmount('1')).toBe(true);
    expect(isPositiveAmount('12.34')).toBe(true);
    expect(isPositiveAmount('0.00000001')).toBe(true);
    expect(isPositiveAmount('0')).toBe(false);
    expect(isPositiveAmount('0.00')).toBe(false);
    expect(isPositiveAmount('-0.5')).toBe(false);
    expect(isPositiveAmount('')).toBe(false);
    expect(isPositiveAmount(null)).toBe(false);
    expect(isPositiveAmount(undefined)).toBe(false);
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

describe('注文の値の丸め（#138 REPLAY-13）', () => {
  it('証拠金は 1〜資金の整数部に収める', () => {
    expect(clampReplayMargin('500', '1000')).toBe(500);
    expect(clampReplayMargin('0', '1000')).toBe(1);
    expect(clampReplayMargin('-20', '1000')).toBe(1);
    expect(clampReplayMargin('1200', '1000')).toBe(1000);
    expect(clampReplayMargin('999', '987.65')).toBe(987);
  });

  it('レバレッジは 1〜現在の上限に収める', () => {
    expect(clampReplayLeverage('10', '25')).toBe(10);
    expect(clampReplayLeverage('0', '25')).toBe(1);
    expect(clampReplayLeverage('30', '25')).toBe(25);
  });

  it('刻みボタンは現在値に足して丸める', () => {
    expect(stepReplayMargin('100', 500, '1000')).toBe('600');
    expect(stepReplayMargin('100', -500, '1000')).toBe('1');
    expect(stepReplayMargin('900', 500, '1000')).toBe('1000');
    expect(stepReplayLeverage('25', 10, '25')).toBe('25');
    expect(stepReplayLeverage('2', -5, '25')).toBe('1');
  });

  it('資金の整数部と注文できるかを返す', () => {
    expect(replayMaxMargin('1000.00000000')).toBe(1000);
    expect(replayMaxMargin('0.5')).toBe(0);
    expect(canReplayOrder('1000')).toBe(true);
    expect(canReplayOrder('0')).toBe(false);
    expect(canReplayOrder('0.00000001')).toBe(false);
  });
});

describe('replayCountdownValue', () => {
  it('5 秒のカウントダウンを 5・4・3・2・1 と出す', () => {
    expect(replayCountdownValue(0)).toBe(5);
    expect(replayCountdownValue(999)).toBe(5);
    expect(replayCountdownValue(1000)).toBe(4);
    expect(replayCountdownValue(1999)).toBe(4);
    expect(replayCountdownValue(2000)).toBe(3);
    expect(replayCountdownValue(3000)).toBe(2);
    expect(replayCountdownValue(4000)).toBe(1);
    expect(replayCountdownValue(4999)).toBe(1);
  });

  it('5 秒を過ぎたら 0（終了）にする', () => {
    expect(replayCountdownValue(5000)).toBe(0);
    expect(replayCountdownValue(9000)).toBe(0);
  });

  it('秒数を変えても同じように数える（reduced-motion でも 5 秒のまま）', () => {
    expect(replayCountdownValue(0, 5)).toBe(5);
    expect(replayCountdownValue(4000, 5)).toBe(1);
    expect(replayCountdownValue(5000, 5)).toBe(0);
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
  it('反対方向の建玉のエラーを日本語にする（#140 REPLAY-14）', () => {
    expect(
      replayErrorMessage(
        { status: 409, data: { message: 'close existing positions of this pair first' } },
        '失敗しました',
      ),
    ).toBe('このペアには反対の向きの建玉があります。先に決済してください');
  });

  it('知らないメッセージはサーバーの文言をそのまま使う', () => {
    expect(
      replayErrorMessage({ status: 409, data: { message: 'some other error' } }, '失敗しました'),
    ).toBe('some other error');
  });

  it('メッセージが無ければ fallback を返す', () => {
    expect(replayErrorMessage(undefined, '失敗しました')).toBe('失敗しました');
  });
});
