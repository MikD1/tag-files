namespace TagFilesService.Library;

public static class VideoDurationParser
{
    public static bool TryParse(string ffmpegOutput, out TimeSpan result)
    {
        try
        {
            string? durationLine = ffmpegOutput
                .Split('\n')
                .FirstOrDefault(line => line.Contains("Duration:"));
            if (durationLine is null)
            {
                throw new ApplicationException("Failed to retrieve video duration");
            }

            string duration = durationLine
                .Split(",")[0]
                .Replace("Duration:", string.Empty)
                .Trim();
            if (!TimeSpan.TryParse(duration, out result))
            {
                throw new ApplicationException("Failed to parse video duration.");
            }

            return true;
        }
        catch (Exception ex)
        {
            Console.WriteLine(ex.Message);
            result = TimeSpan.Zero;
            return false;
        }
    }
}