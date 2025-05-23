using System.Globalization;
using System.Text.RegularExpressions;

namespace TagFilesService.FilesProcessing;

public class VideoConversionProgress
{
    public TimeSpan? Total { get; private set; }

    public TimeSpan? Current { get; private set; }

    public int Percent { get; private set; }

    public void AddOutputLine(string line)
    {
        if (Total == null)
        {
            Match durationMatch = DurationRegex.Match(line);
            if (durationMatch.Success)
            {
                Total = ParseTimeSpan(durationMatch);
            }

            return;
        }

        Match progressMatch = ProgressRegex.Match(line);
        if (!progressMatch.Success)
        {
            return;
        }

        Current = ParseTimeSpan(progressMatch);
        Percent = (int)(Current.Value.TotalSeconds / Total.Value.TotalSeconds * 100);
    }

    private static TimeSpan ParseTimeSpan(Match match)
    {
        int hours = int.Parse(match.Groups[1].Value);
        int minutes = int.Parse(match.Groups[2].Value);
        double seconds = double.Parse(match.Groups[3].Value, CultureInfo.InvariantCulture);
        return new(0, hours, minutes, (int)seconds, (int)((seconds - (int)seconds) * 1000));
    }

    private static readonly Regex DurationRegex = new(@"Duration:\s+(\d+):(\d+):(\d+\.\d+)", RegexOptions.Compiled);

    private static readonly Regex ProgressRegex = new(@"time=(\d+):(\d+):(\d+\.\d+)", RegexOptions.Compiled);
}